
const fs = require('fs');
const path = require('path');

const rootDir = 'C:\\Users\\Admin\\AppData\\Roaming\\npm\\node_modules\\@github\\copilot';

function walk(dir) {
  let results = [];
  const list = fs.readdirSync(dir);
  for (const file of list) {
    const fullPath = path.join(dir, file);
    const stat = fs.statSync(fullPath);
    if (stat.isDirectory()) {
      results = results.concat(walk(fullPath));
    } else {
      results.push(fullPath);
    }
  }
  return results;
}

const allFiles = walk(rootDir);
console.log('Total files to search:', allFiles.length);

const targets = [
  'copilot_internal',
  'githubcopilot.com',
  'api.github.com',
  'device/code',
  'vscode-chat',
  'copilot-cli',
  'Editor-Version',
  'Iv1.',
  '01ab8ac94'
];

for (const file of allFiles) {
  try {
    const buf = fs.readFileSync(file);
    for (const target of targets) {
      if (buf.indexOf(Buffer.from(target)) !== -1) {
        console.log('Found "' + target + '" in: ' + file);
      }
    }
  } catch (err) {
    // ignore
  }
}
