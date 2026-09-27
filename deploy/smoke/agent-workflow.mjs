#!/usr/bin/env node
// Live acceptance test: real tool execution, persisted cwd, code edits and commit.
// Creates a disposable repository on the Hermes host; never pushes anything.
import { execFileSync } from 'node:child_process'
import { randomUUID } from 'node:crypto'

const [origin, host = 'crash'] = process.argv.slice(2)
if (!origin) throw new Error('usage: agent-workflow.mjs BACKEND_ORIGIN [HERMES_SSH_HOST]')
const directory = `/tmp/386gpt-agent-smoke-${randomUUID()}`
const quote = (value) => `'${value.replaceAll("'", "'\\''")}'`
function remote(code) {
  try {
    return execFileSync(host === 'local' ? 'python3' : 'ssh', host === 'local' ? ['-c', code] : ['-o', 'BatchMode=yes', host, `python3 -c ${quote(code)}`], {
      encoding: 'utf8', timeout: 30_000, stdio: ['ignore', 'pipe', 'pipe'],
    }).trim()
  } catch (error) {
    throw new Error(error.stderr?.trim() || error.message)
  }
}
const fixture = String.raw`
from pathlib import Path
import subprocess
p = Path(${JSON.stringify(directory)})
p.mkdir(mode=0o700)
(p / 'names.py').write_text('def normalize_name(value):\n    raise NotImplementedError\n')
(p / 'test_names.py').write_text('''import unittest
from names import normalize_name
class NamesTest(unittest.TestCase):
    def test_trim_and_lower(self):
        self.assertEqual(normalize_name("  ALIce  "), "alice")
    def test_empty(self):
        self.assertEqual(normalize_name("  "), "")
    def test_internal_spaces(self):
        self.assertEqual(normalize_name("  A B  "), "a b")
if __name__ == "__main__":
    unittest.main()
''')
def git(*args):
    subprocess.run(['git', '-C', str(p), *args], check=True, capture_output=True)
git('init', '-b', 'main')
git('config', 'user.name', '386GPT Smoke Test')
git('config', 'user.email', 'smoke@example.invalid')
(p / '.gitignore').write_text('__pycache__/\n')
git('add', '.')
git('commit', '-m', 'Initial fixture')
print(p)
`

let thread
async function turn(content) {
  const socket = new WebSocket(`${origin.replace(/^http/, 'ws')}/ws?thread_id=${encodeURIComponent(thread.id)}`)
  let tools = 0
  try {
    const reply = await new Promise((resolve, reject) => {
      const timer = setTimeout(() => finish(new Error('Agent workflow timed out after 10 minutes')), 600_000)
      function finish(error, value) {
        clearTimeout(timer)
        if (error) reject(error)
        else resolve(value)
      }
      socket.addEventListener('message', ({ data }) => {
        const event = JSON.parse(data)
        if (event.type === 'ready') socket.send(JSON.stringify({ type: 'user_message', content }))
        if (event.type === 'agent_activity' && event.activity?.state === 'started') {
          tools++
          console.log(`Tool: ${event.activity.tool}`)
        }
        if (event.type === 'error') finish(new Error(event.error))
        if (event.type === 'message' && event.message?.role === 'assistant' &&
            event.message.streaming !== true && event.message.content?.trim()) {
          finish(null, event.message.content)
        }
      })
      socket.addEventListener('error', () => finish(new Error('WebSocket failed')))
      socket.addEventListener('close', () => finish(new Error('WebSocket closed before completion')))
    })
    console.log(reply)
    if (!tools) throw new Error('Agent replied without executing tools')
  } finally {
    socket.close()
  }
}

try {
  console.log(`Fixture: ${remote(fixture)}`)
  const response = await fetch(`${origin}/api/threads`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title: 'Agent implementation smoke test' }),
  })
  if (!response.ok) throw new Error(`Create thread: HTTP ${response.status}`)
  ;({ thread } = await response.json())
  console.log(`Hermes transcript: 386gpt-${thread.id}`)
  await turn(`Use your terminal to cd into ${directory}, inspect the Python source and tests, and run the tests. This is a disposable repository. For this turn, report the initial result without changing files.`)
  await turn('Continue in the same repository. Create branch feat/normalize-name and implement normalize_name: strip leading/trailing whitespace and lowercase the string, preserving internal spaces. Run the tests, fix failures, and commit the implementation. Do not push. Do the work now.')
  console.log(remote(`
from pathlib import Path
import subprocess
p = Path(${JSON.stringify(directory)})
def git(*args):
    return subprocess.check_output(['git', '-C', str(p), *args], text=True).strip()
assert git('branch', '--show-current') == 'feat/normalize-name', 'Requested branch missing'
assert int(git('rev-list', '--count', 'HEAD')) >= 2, 'Implementation commit missing'
assert 'names.py' in git('diff', '--name-only', 'main..HEAD').splitlines(), 'No committed implementation'
assert not git('status', '--porcelain'), 'Uncommitted changes remain'
# Use an independent contract so changing the fixture tests cannot fake success.
subprocess.run(['python3', '-c', 'from names import normalize_name as f; assert f("  ALIce  ") == "alice"; assert f("  ") == ""; assert f("  A B  ") == "a b"'], cwd=p, check=True)
subprocess.run(['python3', '-m', 'unittest', '-v'], cwd=p, check=True)
print('Agent workflow passed: real tools, implementation, tests, branch and commit verified.')
`))
} finally {
  if (thread) await fetch(`${origin}/api/threads/${encodeURIComponent(thread.id)}`, { method: 'DELETE' })
  // Remove only this invocation's disposable fixture.
  remote(`import shutil; shutil.rmtree(${JSON.stringify(directory)}, ignore_errors=True)`)
}
