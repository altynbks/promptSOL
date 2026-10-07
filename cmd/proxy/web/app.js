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
const retryKeys = {
  signature: 'solana-x402-retry-signature',
  prompt: 'solana-x402-retry-prompt',
  maxTokens: 'solana-x402-retry-max-tokens',
};
const legacyRetryKeys = {
  signature: 'proofpilot-retry-signature',
  prompt: 'proofpilot-retry-prompt',
  maxTokens: 'proofpilot-retry-max-tokens',
};
for (const key of Object.keys(retryKeys)) {
  const current = sessionStorage.getItem(retryKeys[key]);
  const legacy = sessionStorage.getItem(legacyRetryKeys[key]);
  if (!current && legacy) sessionStorage.setItem(retryKeys[key], legacy);
  sessionStorage.removeItem(legacyRetryKeys[key]);
}
let retryPaymentSignature = sessionStorage.getItem(retryKeys.signature) ?? '';
let quoteController = null;
let quoteTimer = null;
if (retryPaymentSignature) {
  const savedPrompt = sessionStorage.getItem(retryKeys.prompt);
  const savedLimit = sessionStorage.getItem(retryKeys.maxTokens);
  if (savedPrompt) promptInput.value = savedPrompt;
  const savedBudget = savedLimit === '512' ? '384' : savedLimit;
  const savedLength = [...document.querySelectorAll('.length-option')].find((option) => option.dataset.maxTokens === savedBudget);
  if (savedLength) selectAnswerLength(savedLength);
}

function provider() {
  const candidate = window.phantom?.solana ?? window.solana;
  return candidate?.isPhantom ? candidate : null;
}

function setStatus(message, kind = 'idle') {
  statusBox.dataset.kind = kind;
  statusText.textContent = message;
}

function showProvider(provider, model = '') {
  const providerName = provider || 'AI';
  const shortModel = model.split('/').pop().replace(/[-_]/g, ' ').trim();
  $('#provider-label').textContent = providerName.toUpperCase();
  $('#model-label').textContent = shortModel ? shortModel.toUpperCase() : 'AUTO ROUTE';
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
  askButton.querySelector('.button-copy').textContent = retryPaymentSignature ? 'Retry answer (no new payment)' : 'Pay & get your answer';
  setStatus(retryPaymentSignature ? 'A previous confirmed payment is saved for retry. No new transfer will be sent.' : 'Wallet connected. Make sure Phantom is set to Solana Devnet.', 'success');
}

async function connectWallet() {
  if (connectButton.disabled) return;
  const solana = provider();
  if (!solana) {
    setStatus('Phantom was not found. Install the Phantom extension, then refresh this page.', 'error');
    window.open('https://phantom.app/', '_blank', 'noopener,noreferrer');
    return;
  }
  connectButton.disabled = true;
  connectButton.classList.add('is-loading');
  connectButton.setAttribute('aria-busy', 'true');
  walletLabel.textContent = 'Connecting…';
  setStatus('Check Phantom to approve the wallet connection…', 'working');
  try {
    const result = await solana.connect();
    setWallet(result.publicKey ?? solana.publicKey);
  } catch (error) {
    setStatus(error.message || 'Could not connect to Phantom.', 'error');
  } finally {
    connectButton.disabled = false;
    connectButton.classList.remove('is-loading');
    connectButton.removeAttribute('aria-busy');
    if (!wallet) walletLabel.textContent = 'Connect wallet';
  }
}

function parseError(text, fallback) {
  try {
    const value = JSON.parse(text);
    return value.error?.message || value.error || value.message || fallback;
  } catch {
    return text || fallback;
  }
}

const baseSystemPrompt = 'Answer in the user’s language. Follow any explicit constraints in the question. Be clear, accurate, and useful; avoid filler and repetition.';

function answerInstruction(maxTokens) {
  if (maxTokens <= 384) {
    return `${baseSystemPrompt} Keep it brief: answer in one short paragraph of about 70–100 words. Give the direct answer first and include only essential context.`;
  }
  if (maxTokens <= 1024) {
    return `${baseSystemPrompt} Give a balanced explanation of about 200–300 words in 3–4 paragraphs. Explain the key idea, how it works, and add a concrete example plus a relevant benefit or limitation.`;
  }
  return `${baseSystemPrompt} Give a detailed answer of about 450–650 words in 5–7 well-organized paragraphs or short sections. Cover context, the key steps or reasoning, a concrete example, trade-offs, and practical takeaways. Keep every section relevant.`;
}
function selectAnswerLength(selected) {
  document.querySelectorAll('.length-option').forEach((option) => {
    const active = option === selected;
    option.classList.toggle('selected', active);
    option.setAttribute('aria-pressed', String(active));
  });
}

function createPayload(prompt) {
  const answerLimit = document.querySelector('.length-option[aria-pressed="true"]');
  const maxTokens = Number(answerLimit?.dataset.maxTokens ?? 1024);
  return {
    model: 'gemini-3.8-flash',
    reasoning_effort: 'low',
    max_tokens: maxTokens,
    messages: [
      { role: 'system', content: answerInstruction(maxTokens) },
      { role: 'user', content: prompt },
    ],
    stream: true,
  };
}

async function requestQuote(payload) {
  quoteController?.abort();
  quoteController = new AbortController();
  const response = await fetch('/api/quote', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
    signal: quoteController.signal,
  });
  if (!response.ok) throw new Error(parseError(await response.text(), `Could not calculate quote (HTTP ${response.status}).`));
  const quote = await response.json();
  const lamports = Number(quote.amount);
  if (!quote.pay_to || !Number.isSafeInteger(lamports) || lamports < 1) {
    throw new Error('The proxy returned an invalid token estimate.');
  }
  $('#price').textContent = (lamports / 1_000_000_000).toFixed(8).replace(/0+$/, '').replace(/\.$/, '');
  $('#lamports').textContent = `${lamports.toLocaleString('en-US')} lamports`;
  $('#quote-detail').textContent = 'Calculated from your question and answer length. This is the full request price.';
  $('#recipient').textContent = shortAddress(quote.pay_to);
  $('#recipient').title = quote.pay_to;
  $('#recipient').dataset.address = quote.pay_to;
  $('#copy-address').disabled = false;
  return quote;
}

function scheduleQuote() {
  clearTimeout(quoteTimer);
  const prompt = promptInput.value.trim();
  if (!prompt) {
    quoteController?.abort();
    $('#price').textContent = '—';
    $('#lamports').textContent = 'Enter a prompt to see the estimate';
    $('#quote-detail').textContent = 'Your price updates with the question and answer length.';
    return;
  }
  quoteTimer = setTimeout(() => {
    requestQuote(createPayload(prompt)).catch((error) => {
      if (error.name !== 'AbortError') {
        $('#lamports').textContent = 'Price unavailable — retry shortly';
        $('#quote-detail').textContent = 'The price will be confirmed again before payment.';
      }
    });
  }, 300);
}

async function streamAnswer(response, providerName) {
  responsePanel.classList.remove('hidden');
  responseText.textContent = '';
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let firstToken = true;
  let finished = false;
  let receivedDone = false;
  let finishReason = null;
  while (!finished) {
    const { value, done } = await reader.read();
    buffer += decoder.decode(value ?? new Uint8Array(), { stream: !done });
    const lines = buffer.split('\n');
    buffer = lines.pop() ?? '';
    if (done && buffer.trim()) lines.push(buffer);
    for (const line of lines) {
      if (!line.startsWith('data:')) continue;
      const data = line.slice(5).trim();
      if (!data) continue;
      if (data === '[DONE]') {
        receivedDone = true;
        continue;
      }
      try {
        const chunk = JSON.parse(data);
        const choice = chunk.choices?.[0];
        if (choice?.finish_reason) finishReason = choice.finish_reason;
        const rawContent = choice?.delta?.content ?? '';
        const content = typeof rawContent === 'string'
          ? rawContent
          : Array.isArray(rawContent)
            ? rawContent.map((part) => part.text ?? '').join('')
            : '';
        if (content) {
          responseText.textContent += content;
          if (firstToken) {
            setStatus(`${providerName} is writing your answer…`, 'working');
            firstToken = false;
          }
        }
      } catch {
        // Ignore incomplete or non-chat SSE frames.
      }
    }
    finished = done;
  }
  if (!receivedDone) {
    throw new Error('The AI stream ended before the answer was complete. Your payment is saved; retry without paying again.');
  }
  if (!responseText.textContent.trim()) {
    throw new Error('Gemini returned an empty response. Try again with a more specific question.');
  }
  if (finishReason === 'length') {
    throw new Error('The answer reached its length limit. Try a more focused question for a complete response.');
  }
}

async function runDemo() {
  if (!wallet) return connectWallet();
  if (busy) return;
  const prompt = promptInput.value.trim();
  if (!prompt) {
    setStatus('Enter a question before continuing.', 'error');
    promptInput.focus();
    return;
  }
  if (!solanaWeb3) {
    setStatus('Could not load the Solana library. Refresh the page and try again.', 'error');
    return;
  }

  busy = true;
  askButton.disabled = true;
  askButton.classList.add('is-loading');
  askButton.setAttribute('aria-busy', 'true');
  promptInput.disabled = true;
  document.querySelectorAll('.length-option').forEach((option) => { option.disabled = true; });
  responsePanel.classList.add('hidden');
  const buttonCopy = askButton.querySelector('.button-copy');
  const payload = createPayload(prompt);

  let upstreamWaitTimer;
  let quote = null;
  try {
    let signature = retryPaymentSignature;
    const isPaymentRetry = Boolean(signature);
    setStatus('Calculating your token-based quote…', 'working');
    clearTimeout(quoteTimer);
    quote = await requestQuote(payload);
    const lamports = Number(quote.amount);
    if (!signature) {
      const recipient = quote.pay_to;
      const solana = provider();
      const connection = new solanaWeb3.Connection('https://api.devnet.solana.com', 'confirmed');
      let recipientKey;
      try {
        recipientKey = new solanaWeb3.PublicKey(recipient);
      } catch {
        throw new Error('The configured recipient address is invalid. Set SOLANA_WALLET_ADDRESS to a valid 32-byte Solana public key in .env, then restart the proxy.');
      }
      const balance = await connection.getBalance(wallet, 'confirmed');
      const needed = lamports + 10_000;
      if (balance < needed) {
        throw new Error(`This Devnet wallet has ${(balance / 1e9).toFixed(6)} SOL; at least ${(needed / 1e9).toFixed(6)} SOL is needed, including fees. Get test SOL at faucet.solana.com.`);
      }
      const { blockhash, lastValidBlockHeight } = await connection.getLatestBlockhash('confirmed');
      const transaction = new solanaWeb3.Transaction({ feePayer: wallet, recentBlockhash: blockhash })
        .add(solanaWeb3.SystemProgram.transfer({ fromPubkey: wallet, toPubkey: recipientKey, lamports }));

      setStatus(`Approve the ${(lamports / 1_000_000_000).toFixed(8)} SOL transfer in Phantom…`, 'working');
      buttonCopy.textContent = 'Waiting for Phantom…';
      const result = await solana.signAndSendTransaction(transaction, { preflightCommitment: 'confirmed' });
      signature = typeof result === 'string' ? result : (result.signature?.length ? (typeof result.signature === 'string' ? result.signature : base58(result.signature)) : '');
      if (!signature) throw new Error('Phantom did not return a transaction signature.');
      setStatus('Payment submitted. Waiting for Solana confirmation…', 'working');
      const confirmation = await connection.confirmTransaction({ signature, blockhash, lastValidBlockHeight }, 'confirmed');
      if (confirmation.value.err) throw new Error('The Solana network rejected the transaction.');
      retryPaymentSignature = signature;
      sessionStorage.setItem(retryKeys.signature, signature);
      sessionStorage.setItem(retryKeys.prompt, prompt);
      sessionStorage.setItem(retryKeys.maxTokens, String(payload.max_tokens));
    } else {
      setStatus('Retrying with your confirmed payment. No new transfer is needed…', 'working');
    }

    // Phantom is no longer involved after confirmation; show that the answer is generating.
    buttonCopy.textContent = 'Generating answer…';
    const activeProvider = quote.ai_provider || 'AI provider';
    showProvider(`${activeProvider} + fallback`, quote.ai_model || '');
    setStatus(isPaymentRetry ? 'Using your previously confirmed payment. No new transfer is being sent…' : `Payment confirmed. ${activeProvider} is preparing your answer…`, 'working');
    $('#response-meta').textContent = `Paid · ${shortAddress(signature)}`;
    upstreamWaitTimer = setTimeout(() => setStatus('AI is working on your response. The first words may take a few seconds…', 'working'), 8000);

    const answer = await fetch('/v1/chat/completions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Payment-Signature': signature },
      body: JSON.stringify(payload),
    });
    clearTimeout(upstreamWaitTimer);
    if (!answer.ok) {
      const details = parseError(await answer.text(), `Request failed (HTTP ${answer.status}).`);
      if (answer.status === 429 || answer.status >= 500) {
        setStatus('The AI provider is still unavailable. Retry the request without paying again.', 'error');
        throw new Error(details);
      }
      retryPaymentSignature = '';
      sessionStorage.removeItem(retryKeys.signature);
      sessionStorage.removeItem(retryKeys.prompt);
      sessionStorage.removeItem(retryKeys.maxTokens);
      throw new Error(details);
    }

    const aiProvider = answer.headers.get('X-AI-Provider') || 'AI provider';
    const aiModel = answer.headers.get('X-AI-Model') || 'AI model';
    showProvider(aiProvider, aiModel);
    $('#response-meta').textContent = `Paid · ${shortAddress(signature)} · ${aiProvider}`;
    setStatus(`${aiProvider} is streaming your answer…`, 'working');
    await streamAnswer(answer, aiProvider);
    // Keep the payment proof available until the stream is fully parsed. If the
    // provider drops a stream mid-answer, the user can retry without paying again.
    retryPaymentSignature = '';
    sessionStorage.removeItem(retryKeys.signature);
    sessionStorage.removeItem(retryKeys.prompt);
    sessionStorage.removeItem(retryKeys.maxTokens);
    $('#response-meta').textContent = `${aiProvider} · Answer ready`;
    setStatus('Complete. Your payment is confirmed and your answer is ready.', 'success');
  } catch (error) {
    clearTimeout(upstreamWaitTimer);
    const message = error?.message || 'The request could not be completed.';
    setStatus(retryPaymentSignature && !message.includes('User rejected') ? `The AI provider is unavailable. Your payment is saved; click “Retry answer” to try again without paying. ${message}` : message.includes('User rejected') ? 'Transaction cancelled in Phantom.' : /Blockhash not found/i.test(message) ? 'The transaction blockhash expired. Click again and approve the payment promptly.' : message, 'error');
  } finally {
    busy = false;
    askButton.disabled = false;
    askButton.classList.remove('is-loading');
    askButton.removeAttribute('aria-busy');
    promptInput.disabled = false;
    document.querySelectorAll('.length-option').forEach((option) => { option.disabled = false; });
    buttonCopy.textContent = retryPaymentSignature ? 'Retry answer (no new payment)' : 'Pay & get your answer';
  }
}
connectButton.addEventListener('click', connectWallet);
askButton.addEventListener('click', runDemo);
promptInput.addEventListener('input', () => {
  $('#char-count').textContent = `${promptInput.value.length} / 1000`;
  scheduleQuote();
});
document.querySelectorAll('.length-option').forEach((option) => {
  option.addEventListener('click', () => {
    selectAnswerLength(option);
    scheduleQuote();
  });
});
$('#copy-address').addEventListener('click', async () => {
  const recipient = $('#recipient').dataset.address;
  if (!recipient) return;
  await navigator.clipboard.writeText(recipient);
  setStatus('Recipient address copied.', 'success');
});
promptInput.addEventListener('keydown', (event) => {
  if (event.key === 'Enter' && !event.shiftKey) {
    event.preventDefault();
    runDemo();
  }
});
$('#char-count').textContent = `${promptInput.value.length} / 1000`;
scheduleQuote();

if (provider()?.publicKey) setWallet(provider().publicKey);
