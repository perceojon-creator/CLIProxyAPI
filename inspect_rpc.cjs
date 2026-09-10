
const fs = require('fs');

const rpcDts = fs.readFileSync('C:\\Users\\Admin\\AppData\\Roaming\\npm\\node_modules\\@github\\copilot\\node_modules\\@github\\copilot-win32-x64\\copilot-sdk\\generated\\rpc.d.ts', 'utf8');
const rpcLines = rpcDts.split('\n');
rpcLines.forEach((l, i) => {
  if (l.includes('copilot_internal') || l.includes('token') || l.includes('auth')) {
    console.log((i+1) + ': ' + l);
  }
});

const schema = fs.readFileSync('C:\\Users\\Admin\\AppData\\Roaming\\npm\\node_modules\\@github\\copilot\\node_modules\\@github\\copilot-win32-x64\\schemas\\api.schema.json', 'utf8');
const schemaLines = schema.split('\n');
schemaLines.forEach((l, i) => {
  if (l.includes('copilot_internal') || l.includes('endpoints')) {
    console.log('Schema ' + (i+1) + ': ' + l);
  }
});
