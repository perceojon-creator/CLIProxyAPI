
const https = require('https');
const { execSync } = require('child_process');

const token = execSync('gh auth token').toString().trim();

function testToken(authHeader, userAgent, editorVersion, editorPluginVersion) {
  return new Promise((resolve) => {
    const headers = {
      'Authorization': authHeader,
      'Accept': 'application/json',
      'User-Agent': userAgent || 'GitHubCopilotChat/0.24.0',
    };
    if (editorVersion) headers['Editor-Version'] = editorVersion;
    if (editorPluginVersion) headers['Editor-Plugin-Version'] = editorPluginVersion;

    const req = https.request('https://api.github.com/copilot_internal/v2/token', {
      method: 'GET',
      headers: headers
    }, (res) => {
      let body = '';
      res.on('data', chunk => body += chunk);
      res.on('end', () => {
        resolve({
          statusCode: res.statusCode,
          headers: res.headers,
          body: body
        });
      });
    });
    req.on('error', (err) => resolve({ error: err.message }));
    req.end();
  });
}

(async () => {
  console.log("--- Test 1: token <token>, GithubCopilot/1.0.83 ---");
  const res1 = await testToken('token ' + token, 'GithubCopilot/1.0.83', 'CopilotCLI/1.0.83', 'copilot-cli/1.0.83');
  console.log('Status 1:', res1.statusCode);
  if (res1.body) {
    try {
      const parsed = JSON.parse(res1.body);
      const sanitized = { ...parsed };
      if (sanitized.token) {
        sanitized.token = sanitized.token.substring(0, 20) + '...[REDACTED]...;exp=' + (sanitized.expires_at || '');
      }
      console.log('Body 1 keys:', Object.keys(parsed));
      console.log('Sanitized Body 1:', JSON.stringify(sanitized, null, 2));
    } catch {
      console.log('Raw body 1:', res1.body.substring(0, 300));
    }
  }

  console.log("--- Test 2: Bearer <token>, vscode ---");
  const res2 = await testToken('Bearer ' + token, 'GitHubCopilotChat/0.24.0', 'vscode/1.96.0', 'copilot-chat/0.24.0');
  console.log('Status 2:', res2.statusCode);
  if (res2.body) {
    try {
      const parsed = JSON.parse(res2.body);
      const sanitized = { ...parsed };
      if (sanitized.token) {
        sanitized.token = sanitized.token.substring(0, 20) + '...[REDACTED]...;exp=' + (sanitized.expires_at || '');
      }
      console.log('Body 2 keys:', Object.keys(parsed));
      console.log('Sanitized Body 2:', JSON.stringify(sanitized, null, 2));
    } catch {
      console.log('Raw body 2:', res2.body.substring(0, 300));
    }
  }
})();
