let solanaWeb3 = null;
try {
  const bufferModule = await import('https://esm.sh/buffer@6.0.3?bundle');
  globalThis.Buffer ??= bufferModule.Buffer;
  solanaWeb3 = await import('https://esm.sh/@solana/web3.js@1.98.0?bundle');
} catch (error) {
  console.error('Could not load Solana browser libraries:', error);
}

const $ = (selector) => document.querySelector(selector);
const connectButton = $('#connect-button');
const walletLabel = $('#wallet-label');
const askButton = $('#ask-button');
const promptInput = $('#prompt');
const statusBox = $('#status');
const statusText = $('#status-text');
const retryButton = $('#retry-button');
const responsePanel = $('#response-panel');
const responseText = $('#response-text');
const flowSteps = [...document.querySelectorAll('.step[data-step]')];
const DEMO_SYSTEM_PROMPT = 'Отвечай на языке пользователя и сразу на его вопрос. Если пользователь просит объяснить понятие или принцип, объясни простыми словами в 3–5 коротких предложениях и приведи один конкретный пример. Не подменяй термин похожей аббревиатурой; если вопрос неоднозначен, явно обозначь трактовку.';
let wallet = null;
let busy = false;
let pendingQuote = null;
let pendingDelivery = null;

function setFlowStep(currentStep) {
  for (const step of flowSteps) {
    const index = Number(step.dataset.step);
    const state = index < currentStep ? 'complete' : index === currentStep ? 'current' : 'upcoming';
    step.dataset.state = state;
    if (state === 'current') step.setAttribute('aria-current', 'step');
    else step.removeAttribute('aria-current');
    step.querySelector('.step-check').textContent = state === 'complete' ? '✓' : state === 'current' ? '●' : '';
  }
}

function provider() {
  const candidate = window.phantom?.solana ?? window.solana;
  return candidate?.isPhantom ? candidate : null;
}

function setStatus(message, kind = 'idle', canRetry = false, retryLabel = 'Повторить') {
  statusBox.dataset.kind = kind;
  statusBox.setAttribute('aria-busy', kind === 'working' ? 'true' : 'false');
  statusText.textContent = message;
  retryButton.classList.toggle('hidden', !canRetry);
  retryButton.textContent = retryLabel;
}

function shortAddress(value) {
  return `${value.slice(0, 4)}…${value.slice(-4)}`;
}

function base58(bytes) {
  const alphabet = '123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz';
  let number = 0n;
  for (const byte of bytes) number = number * 256n + BigInt(byte);
  let output = '';
  while (number > 0n) {
    const remainder = Number(number % 58n);
    output = alphabet[remainder] + output;
    number /= 58n;
  }
  for (const byte of bytes) {
    if (byte !== 0) break;
    output = `1${output}`;
  }
  return output;
}

function setWallet(publicKey) {
  wallet = publicKey;
  walletLabel.textContent = shortAddress(publicKey.toString());
  connectButton.classList.add('connected');
  askButton.querySelector('.button-copy').textContent = 'Рассчитать цену';
  setFlowStep(1);
  setStatus('Phantom подключён. Шаг 2: введите вопрос — точная цена появится до подтверждения.', 'idle');
}

async function connectWallet() {
  const solana = provider();
  if (!solana) {
    setStatus('Phantom не найден. Установите расширение Phantom и повторите подключение.', 'error', true, 'Повторить подключение');
    window.open('https://phantom.app/', '_blank', 'noopener,noreferrer');
    return;
  }
  try {
    const result = await solana.connect();
    setWallet(result.publicKey ?? solana.publicKey);
  } catch (error) {
    setStatus(error.message || 'Не удалось подключить кошелёк.', 'error', true, 'Повторить подключение');
  }
}

function parseError(text, fallback) {
  try {
    const value = JSON.parse(text);
    return value.error || value.message || fallback;
  } catch {
    return text || fallback;
  }
}

function showQuote(quote) {
  $('#summary-label').textContent = `К ОПЛАТЕ · ${quote.tokenCount.toLocaleString('ru-RU')} ТОКЕНОВ`;
  $('#price').textContent = quote.amount.toLocaleString('ru-RU');
  $('#price-unit').textContent = 'lamports';
  $('#price-sol').textContent = `${(quote.amount / 1_000_000_000).toFixed(8)} SOL`;
}

async function deliverPaidResponse() {
  if (!pendingDelivery) return;
  const { paymentId, signature, signedTransaction, blockhash, lastValidBlockHeight, payload, tokenCount } = pendingDelivery;
  const connection = new solanaWeb3.Connection('https://api.devnet.solana.com', 'confirmed');
  setFlowStep(2);
  setStatus('Проверяем и отправляем транзакцию в Devnet…', 'working');
  await connection.sendRawTransaction(signedTransaction.serialize(), { preflightCommitment: 'confirmed' }).catch(() => {});
  const confirmation = await connection.confirmTransaction({ signature, blockhash, lastValidBlockHeight }, 'confirmed');
  if (confirmation.value.err) throw new Error('Devnet отклонил платёж. Проверьте сеть и повторите сценарий.');

  setFlowStep(3);
  setStatus('Платёж подтверждён. Получаем оплаченный ответ…', 'working');
  const answer = await fetch('/v1/chat/completions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Payment-Signature': signature, 'X-Payment-ID': paymentId },
    body: JSON.stringify(payload),
  });
  if (!answer.ok) throw new Error(parseError(await answer.text(), `Не удалось получить оплаченный ответ (HTTP ${answer.status}).`));
  const completion = await answer.json();
  const content = completion.choices?.[0]?.message?.content;
  if (typeof content !== 'string' || !content.trim()) throw new Error('Провайдер вернул пустой ответ.');
  responseText.textContent = content;
  responsePanel.classList.remove('hidden');
  $('#response-meta').textContent = `Оплачено · ${tokenCount.toLocaleString('ru-RU')} токенов · ${shortAddress(signature)}`;
  pendingDelivery = null;
  pendingQuote = null;
  setFlowStep(flowSteps.length);
  setStatus(`Готово. Оплачено ${tokenCount.toLocaleString('ru-RU')} фактических токенов, ответ получен.`, 'success');
}

async function submitQuotedPayment(quote) {
  if (Date.now() >= quote.expiresAt) throw new Error('QUOTE_EXPIRED');
  showQuote(quote);
  setFlowStep(2);
  setStatus(`Цена рассчитана: ${quote.tokenCount.toLocaleString('ru-RU')} токенов. Готовим перевод в Phantom…`, 'working');
  const connection = new solanaWeb3.Connection('https://api.devnet.solana.com', 'confirmed');
  const balance = await connection.getBalance(wallet, 'confirmed');
  const needed = quote.amount + 10_000;
  if (balance < needed) {
    throw new Error(`На Devnet недостаточно тестовых SOL для перевода и комиссии. Нужно около ${(needed / 1e9).toFixed(6)} SOL.`);
  }
  const recipientKey = new solanaWeb3.PublicKey(quote.recipient);
  const blockhashInfo = await connection.getLatestBlockhash('confirmed');
  const transaction = new solanaWeb3.Transaction({ feePayer: wallet, recentBlockhash: blockhashInfo.blockhash })
    .add(solanaWeb3.SystemProgram.transfer({ fromPubkey: wallet, toPubkey: recipientKey, lamports: quote.amount }));

  setStatus(`Подтвердите оплату ${quote.tokenCount.toLocaleString('ru-RU')} токенов в Phantom · только Devnet…`, 'working');
  const solana = provider();
  if (typeof solana?.signTransaction !== 'function') throw new Error('Phantom не поддерживает подпись для Devnet. Обновите кошелёк.');
  const signedTransaction = await solana.signTransaction(transaction);
  const signature = signedTransaction.signature?.length ? base58(signedTransaction.signature) : '';
  if (!signature) throw new Error('Phantom не вернул подпись транзакции.');
  if (Date.now() >= quote.expiresAt) throw new Error('QUOTE_EXPIRED');

  // Broadcasting is pinned to Devnet even if Phantom has another network selected.
  pendingDelivery = {
    paymentId: quote.paymentId,
    signature,
    signedTransaction,
    blockhash: blockhashInfo.blockhash,
    lastValidBlockHeight: blockhashInfo.lastValidBlockHeight,
    payload: quote.payload,
    tokenCount: quote.tokenCount,
  };
  await deliverPaidResponse();
}

async function runDemo() {
  if (!wallet) return connectWallet();
  if (busy) return;
  if (pendingQuote && Date.now() >= pendingQuote.expiresAt) {
    pendingQuote = null;
    promptInput.disabled = false;
    setStatus('Расчёт истёк до оплаты. Нажмите кнопку, чтобы запросить новый расчёт; AI-запрос выполнится повторно.', 'error', true, 'Пересчитать');
    return;
  }
  const prompt = promptInput.value.trim();
  if (!pendingDelivery && !pendingQuote && !prompt) {
    setStatus('Введите вопрос, чтобы продолжить.', 'error', true, 'К вопросу');
    promptInput.focus();
    return;
  }
  if (!solanaWeb3) {
    setStatus('Не удалось загрузить библиотеку Solana. Обновите страницу и повторите попытку.', 'error', true, 'Повторить');
    return;
  }

  busy = true;
  askButton.disabled = true;
  promptInput.disabled = true;
  responsePanel.classList.add('hidden');
  const payload = pendingDelivery?.payload ?? pendingQuote?.payload ?? {
    model: 'gemini-3.8-flash',
    messages: [
      { role: 'system', content: DEMO_SYSTEM_PROMPT },
      { role: 'user', content: prompt },
    ],
    stream: false,
  };

  try {
    if (pendingDelivery) {
      await deliverPaidResponse();
      return;
    }
    if (pendingQuote) {
      await submitQuotedPayment(pendingQuote);
      return;
    }

    setFlowStep(1);
    setStatus('Считаем точную цену по ответу Gemini. После расчёта вы проверите сумму в Phantom.', 'working');
    const infoResponse = await fetch('/api/payment-info');
    if (!infoResponse.ok) throw new Error(`Не удалось получить реквизиты платежа (HTTP ${infoResponse.status}).`);
    const info = await infoResponse.json();
    const recipient = info.pay_to;
    const rate = Number(info.lamports_per_token);
    const model = info.model;
    if (!recipient || !Number.isSafeInteger(rate) || rate < 1 || typeof model !== 'string' || !model) throw new Error('Прокси вернул некорректные настройки расчёта.');
    payload.model = model;

    $('#recipient').textContent = shortAddress(recipient);
    $('#recipient').title = recipient;
    $('#recipient').dataset.address = recipient;
    $('#model-label').textContent = model.toUpperCase();
    $('#copy-address').disabled = false;
    $('#summary-label').textContent = 'СТАВКА ЗА ТОКЕН';
    $('#price').textContent = rate.toLocaleString('ru-RU');
    $('#price-unit').textContent = 'lamports / токен';
    $('#price-sol').textContent = `${(rate / 1_000_000_000).toFixed(8)} SOL / токен`;
    $('#copy-address').onclick = async () => {
      await navigator.clipboard.writeText(recipient);
      setStatus('Адрес получателя скопирован.', 'success');
    };

    setStatus('Считаем точную стоимость ответа…', 'working');
    const quoteResponse = await fetch('/v1/chat/completions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    if (quoteResponse.status !== 402) {
      throw new Error(parseError(await quoteResponse.text(), `Не удалось получить расчёт (HTTP ${quoteResponse.status}).`));
    }
    const quoteData = await quoteResponse.json();
    const amount = Number(quoteData.amount);
    const tokenCount = Number(quoteData.token_count);
    if (!quoteData.payment_id || quoteData.pay_to !== recipient || !Number.isSafeInteger(amount) || amount < 1 || !Number.isSafeInteger(tokenCount) || tokenCount < 1 || amount !== tokenCount * rate) {
      throw new Error('Прокси вернул некорректный расчёт токенов и суммы. Оплата не отправлена.');
    }
    pendingQuote = {
      paymentId: quoteData.payment_id,
      recipient,
      amount,
      tokenCount,
      payload,
      expiresAt: Date.now() + (Number(quoteData.expires_in_seconds) || 300) * 1000,
    };
    await submitQuotedPayment(pendingQuote);
  } catch (error) {
    if (error?.message === 'QUOTE_EXPIRED') {
      pendingQuote = null;
      promptInput.disabled = false;
      setFlowStep(1);
      setStatus('Расчёт истёк до отправки платежа. Запросите новую цену; это повторно вызовет AI-провайдера.', 'error', true, 'Пересчитать');
      return;
    }
    const message = error?.message || 'Не удалось выполнить запрос.';
    const friendlyMessage = message.includes('User rejected') ? 'Вы отменили транзакцию в Phantom.' : /Blockhash not found/i.test(message) ? 'Блокхеш устарел. Повторите попытку и подтвердите быстрее.' : message;
    const retryMessage = pendingDelivery ? `${friendlyMessage} Повтор продолжит ту же оплату и не создаст новый перевод.` : pendingQuote ? `${friendlyMessage} Повтор использует тот же расчёт без нового запроса к AI.` : friendlyMessage;
    setStatus(retryMessage, 'error', true, pendingDelivery ? 'Повторить получение' : pendingQuote ? 'Повторить оплату' : 'Повторить запрос');
  } finally {
    busy = false;
    askButton.disabled = false;
    promptInput.disabled = Boolean(pendingQuote || pendingDelivery);
  }
}

connectButton.addEventListener('click', connectWallet);
askButton.addEventListener('click', runDemo);
retryButton.addEventListener('click', () => {
  if (!pendingDelivery && !pendingQuote && !promptInput.value.trim()) {
    promptInput.focus();
    return;
  }
  runDemo();
});
promptInput.addEventListener('input', () => { $('#char-count').textContent = `${promptInput.value.length} / 1000`; });
promptInput.addEventListener('keydown', (event) => {
  if (event.key === 'Enter' && !event.shiftKey) {
    event.preventDefault();
    runDemo();
  }
});
$('#char-count').textContent = `${promptInput.value.length} / 1000`;

setFlowStep(0);
if (provider()?.publicKey) setWallet(provider().publicKey);
