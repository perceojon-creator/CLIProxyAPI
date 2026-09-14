
const fs = require('fs');
const path = 'C:\\Users\\Admin\\AppData\\Roaming\\npm\\node_modules\\@github\\copilot\\node_modules\\@github\\copilot-win32-x64\\app.js';
const content = fs.readFileSync(path, 'utf8');

const keywords = [
  'device/code',
  'copilot_internal',
  'githubcopilot.com',
  'chat/completions',
  '/models',
  'client_id',
  'oauth',
  'Editor-Version',
  'Copilot-Integration-Id',
  'Openai-Intent',
  'gho_',
  'tid=',
  'sku='
];

for (const kw of keywords) {
  let count = 0;
  let idx = 0;
  const samples = [];
  while ((idx = content.indexOf(kw, idx)) !== -1) {
    count++;
    if (samples.length < 3) {
      const start = Math.max(0, idx - 120);
      const end = Math.min(content.length, idx + kw.length + 120);
      samples.push(content.substring(start, end).replace(/\n/g, ' '));
    }
    idx += kw.length;
  }
  console.log('=== KEYWORD:', kw, '(found ' + count + ') ===');
  samples.forEach((s, i) => console.log(' [' + i + ']:', s));
}
