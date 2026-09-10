
const https = require('https');
const { execSync } = require('child_process');

const token = execSync('gh auth token').toString().trim();

const req = https.request('https://api.github.com/user', {
  headers: {
    'Authorization': 'Bearer ' + token,
    'User-Agent': 'GitHub-Copilot-Audit/1.0',
    'Accept': 'application/vnd.github.v3+json'
  }
}, (res) => {
  console.log('Status:', res.statusCode);
  console.log('OAuth Scopes:', res.headers['x-oauth-scopes']);
  console.log('Accepted Scopes:', res.headers['x-accepted-oauth-scopes']);
  let body = '';
  res.on('data', d => body += d);
  res.on('end', () => {
    const user = JSON.parse(body);
    console.log('User login:', user.login);
  });
});
req.on('error', (e) => console.error(e));
req.end();
