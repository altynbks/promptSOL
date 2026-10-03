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
const responsePanel = $('#response-panel');
const responseText = $('#response-text');
let wallet = null;
let busy = false;

function provider() {
  const candidate = window.phantom?.solana ?? window.solana;
  return candidate?.isPhantom ? candidate : null;
}

function setStatus(message, kind = 'idle') {
  statusBox.dataset.kind = kind;
  statusText.textContent = message;
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
  askButton.querySelector('.button-copy').textContent = 'Оплатить и получить ответ';
  setStatus('Кошелёк подключён. Убедитесь, что выбрана сеть Devnet.', 'success');
}

async function connectWallet() {
  const solana = provider();
  if (!solana) {
    setStatus('Phantom не найден. Установите расширение Phantom и обновите страницу.', 'error');
    window.open('https://phantom.app/', '_blank', 'noopener,noreferrer');
    return;
  }
  try {
    const result = await solana.connect();
    setWallet(result.publicKey ?? solana.publicKey);
  } catch (error) {
    setStatus(error.message || 'Не удалось подключить кошелёк.', 'error');
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

async function streamAnswer(response) {
  responsePanel.classList.remove('hidden');
  responseText.textContent = '';
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let finished = false;
  while (!finished) {
    const { value, done } = await reader.read();
    buffer += decoder.decode(value ?? new Uint8Array(), { stream: !done });
    const lines = buffer.split('\n');
    buffer = lines.pop() ?? '';
    for (const line of lines) {
      if (!line.startsWith('data:')) continue;
      const data = line.slice(5).trim();
      if (!data || data === '[DONE]') continue;
      try {
        const chunk = JSON.parse(data);
        responseText.textContent += chunk.choices?.[0]?.delta?.content ?? '';
      } catch {
        // Ignore incomplete or non-chat SSE frames.
      }
    }
    finished = done;
  }
}

async function runDemo() {
  if (!wallet) return connectWallet();
  if (busy) return;
  const prompt = promptInput.value.trim();
  if (!prompt) {
    setStatus('Сначала введите вопрос для Gemini.', 'error');
    promptInput.focus();
    return;
  }
  if (!solanaWeb3) {
    setStatus('Не удалось загрузить библиотеку Solana. Обновите страницу.', 'error');
    return;
  }

  busy = true;
  askButton.disabled = true;
  responsePanel.classList.add('hidden');
  const buttonCopy = askButton.querySelector('.button-copy');
  const originalCopy = 'Оплатить и получить ответ';
  const payload = {
    model: 'gemini-3.8-flash',
    messages: [{ role: 'user', content: prompt }],
    stream: true,
  };

  try {
    setStatus('Получаем сумму и адрес для оплаты…', 'working');
    const infoResponse = await fetch('/api/payment-info');
    if (!infoResponse.ok) throw new Error(`Не удалось получить реквизиты платежа (HTTP ${infoResponse.status}).`);
    const info = await infoResponse.json();
    const recipient = info.pay_to;
    const lamports = Number(info.amount);
    if (!recipient || !Number.isSafeInteger(lamports) || lamports < 1) {
      throw new Error('Прокси вернул некорректные реквизиты платежа.');
    }
    $('#recipient').textContent = shortAddress(recipient);
    $('#recipient').title = recipient;
    $('#recipient').dataset.address = recipient;
    $('#copy-address').disabled = false;
    $('#price').textContent = (lamports / 1_000_000_000).toFixed(8).replace(/0+$/, '').replace(/\.$/, '');
    $('#copy-address').onclick = async () => {
      await navigator.clipboard.writeText(recipient);
      setStatus('Адрес получателя скопирован.', 'success');
    };

    const solana = provider();
    const connection = new solanaWeb3.Connection('https://api.devnet.solana.com', 'confirmed');
    const recipientKey = new solanaWeb3.PublicKey(recipient);
    const balance = await connection.getBalance(wallet, 'confirmed');
    const needed = lamports + 10_000; // сумма + запас на комиссию
    if (balance < needed) {
      throw new Error(`На Devnet у кошелька ${shortAddress(wallet.toString())} всего ${(balance / 1e9).toFixed(6)} SOL. Нужно минимум ${(needed / 1e9).toFixed(6)} SOL. Получите SOL на https://faucet.solana.com (сеть Devnet).`);
    }
    const { blockhash, lastValidBlockHeight } = await connection.getLatestBlockhash('confirmed');
    const transaction = new solanaWeb3.Transaction({ feePayer: wallet, recentBlockhash: blockhash })
      .add(solanaWeb3.SystemProgram.transfer({ fromPubkey: wallet, toPubkey: recipientKey, lamports }));

    setStatus(`Подтвердите перевод ${(lamports / 1_000_000_000).toFixed(8)} SOL в Phantom…`, 'working');
    buttonCopy.textContent = 'Ожидание подтверждения в Phantom…';
    // Phantom отправляет в сеть, выбранную в кошельке (должен быть Solana Devnet).
    const result = await solana.signAndSendTransaction(transaction, { preflightCommitment: 'confirmed' });
    const signature = typeof result === 'string' ? result : (result.signature?.length ? (typeof result.signature === 'string' ? result.signature : base58(result.signature)) : '');
    if (!signature) throw new Error('Phantom не вернул подпись транзакции.');
    setStatus('Платёж отправлен. Ждём подтверждение Solana…', 'working');
    const confirmation = await connection.confirmTransaction({ signature, blockhash, lastValidBlockHeight }, 'confirmed');
    if (confirmation.value.err) throw new Error('Транзакция отклонена сетью Solana.');

    setStatus('Оплата подтверждена. Gemini готовит ответ…', 'working');
    $('#response-meta').textContent = `Оплачено · ${shortAddress(signature)}`;
    const answer = await fetch('/v1/chat/completions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Payment-Signature': signature },
      body: JSON.stringify(payload),
    });
    if (!answer.ok) throw new Error(parseError(await answer.text(), `Запрос завершился с HTTP ${answer.status}`));
    setStatus('Ответ поступает в реальном времени.', 'success');
    await streamAnswer(answer);
    setStatus('Готово. Платёж подтверждён, ответ получен.', 'success');
  } catch (error) {
    const message = error?.message || 'Не удалось выполнить запрос.';
    setStatus(message.includes('User rejected') ? 'Вы отменили транзакцию в Phantom.' : /Blockhash not found/i.test(message) ? 'Блокхеш устарел (окно Phantom было открыто слишком долго). Нажмите кнопку ещё раз и подтвердите быстрее.' : message, 'error');
  } finally {
    busy = false;
    askButton.disabled = false;
    buttonCopy.textContent = originalCopy;
  }
}

connectButton.addEventListener('click', connectWallet);
askButton.addEventListener('click', runDemo);
promptInput.addEventListener('input', () => { $('#char-count').textContent = `${promptInput.value.length} / 1000`; });
promptInput.addEventListener('keydown', (event) => {
  if (event.key === 'Enter' && !event.shiftKey) {
    event.preventDefault();
    runDemo();
  }
});
$('#char-count').textContent = `${promptInput.value.length} / 1000`;

if (provider()?.publicKey) setWallet(provider().publicKey);