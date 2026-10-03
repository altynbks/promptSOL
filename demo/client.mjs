import { readFile } from 'node:fs/promises';
import { Connection, Keypair, PublicKey, SystemProgram, Transaction, clusterApiUrl, sendAndConfirmTransaction } from '@solana/web3.js';

const proxyUrl = process.env.PROXY_URL ?? 'http://localhost:8080/v1/chat/completions';
const secretPath = process.env.SOLANA_KEYPAIR_PATH;
if (!secretPath) throw new Error('Set SOLANA_KEYPAIR_PATH to a funded Devnet keypair JSON file');
const payer = Keypair.fromSecretKey(Uint8Array.from(JSON.parse(await readFile(secretPath, 'utf8'))));
const connection = new Connection(process.env.SOLANA_RPC_URL ?? clusterApiUrl('devnet'), 'confirmed');
const payload = { model: process.env.AI_MODEL ?? 'gemini-3.8-flash', messages: [{ role: 'user', content: 'Say hello in one sentence.' }], stream: true };

let response = await fetch(proxyUrl, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) });
if (response.status !== 402) throw new Error(`Expected initial 402, got ${response.status}: ${await response.text()}`);
const payTo = response.headers.get('X-Solana-Pay-To');
const lamports = Number(response.headers.get('X-Payment-Amount'));
if (!payTo || !Number.isSafeInteger(lamports) || lamports < 1) throw new Error('Invalid payment challenge headers');
console.log(`Paying ${lamports} lamports to ${payTo}`);
const tx = new Transaction().add(SystemProgram.transfer({ fromPubkey: payer.publicKey, toPubkey: new PublicKey(payTo), lamports }));
const signature = await sendAndConfirmTransaction(connection, tx, [payer], { commitment: 'confirmed' });
console.log(`Confirmed payment: ${signature}`);

response = await fetch(proxyUrl, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json', 'X-Payment-Signature': signature },
  body: JSON.stringify(payload),
});
if (!response.ok) throw new Error(`Paid request failed (${response.status}): ${await response.text()}`);
if (!response.body) throw new Error('Upstream returned no response body');
for await (const chunk of response.body) process.stdout.write(chunk);
