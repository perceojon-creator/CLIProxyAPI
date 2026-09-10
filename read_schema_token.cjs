
const fs = require('fs');
const schema = fs.readFileSync('C:\\Users\\Admin\\AppData\\Roaming\\npm\\node_modules\\@github\\copilot\\node_modules\\@github\\copilot-win32-x64\\schemas\\api.schema.json', 'utf8');
const lines = schema.split('\n');
console.log(lines.slice(15545, 15850).join('\n'));
