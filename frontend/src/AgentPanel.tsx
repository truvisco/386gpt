import type { Activity, Run, Runtime, Skill } from './useAgent'
import { isActive } from './useAgent'

function display(value: unknown) { return typeof value === 'string' ? value : JSON.stringify(value, null, 2) }
function resultState(value: unknown) {
  try {
    const data = typeof value === 'string' ? JSON.parse(value) : value
    if (data?.error || (typeof data?.exit_code === 'number' && data.exit_code !== 0)) return 'TOOL FAILED'
    if (data?.exit_code === 0) return 'EXIT 0'
  } catch { /* Some tools return plain text. */ }
  return 'RESULT'
}
export function AgentPanel({ run, events, runtime, skills, selected, onSelect, onSkills, onControl, error }: {
  run: Run | null; events: Activity[]; runtime: Runtime; skills: Skill[]; selected: string[];
  onSelect: (names: string[]) => void; onSkills: () => void;
  onControl: (action: string, body?: object) => void; error: string;
}) {
  const calls = events.filter(e => e.kind === 'tool.call')
  const results = new Map(events.filter(e => e.kind === 'tool.result').map(e => [String(e.data.callId), e]))
  const cwd = [...events].reverse().find(e => e.kind === 'tool.result' && typeof e.data.result === 'string' && /"cwd"/.test(e.data.result))
  let workingDirectory = runtime.cwd
  if (cwd) { try { workingDirectory = JSON.parse(String(cwd.data.result)).cwd ?? workingDirectory } catch { /* Keep runtime default. */ } }
  return <section className="agent-panel" aria-label="Agent activity">
    <div className="agent-runtime">TOOLS: {runtime.hostname ?? runtime.id ?? 'UNKNOWN'} · {runtime.os ?? ''} · {runtime.healthy === undefined ? 'CONNECTING' : runtime.healthy ? 'CONNECTED' : 'OFFLINE'}{runtime.transport === "messaging_gateway" && <span>GATEWAY PROFILE: {runtime.profile}</span>}{workingDirectory && <span>WORKDIR: {workingDirectory}</span>}</div>
    {error && <div role="alert">{error}</div>}
    {runtime.error && <div role="alert">{runtime.error}</div>}
    {run && <div className="agent-run-status">{run.status === 'completed' ? 'AGENT FINISHED — inspect tool results below' : run.status.toUpperCase().replaceAll('_', ' ')}{isActive(run) && <button onClick={() => onControl('stop')}>STOP</button>}{run.error && <p role="alert">{run.error}</p>}{run.pendingSteer != null && <pre>Undelivered update: {display(run.pendingSteer)}</pre>}</div>}
    {run?.status === 'waiting_for_approval' && run.approval && <div className="agent-approval" role="alert">
      <strong>APPROVAL REQUIRED</strong><pre>{run.approval.command ?? run.approval.description}</pre>
      {run.approval.choices.map(choice => <button key={choice} onClick={() => onControl('approval', { request_id: run.approval?.request_id, choice })}>{choice.toUpperCase().replaceAll('_', ' ')}</button>)}
    </div>}
    <details className="agent-skills" onToggle={e => { if (e.currentTarget.open && !skills.length) onSkills() }}>
      <summary>SKILLS {selected.length > 0 && `(${selected.length} selected)`}</summary>
      {skills.map(skill => <label key={skill.name} title={skill.description}><input type="checkbox" checked={selected.includes(skill.name)} onChange={e => onSelect(e.target.checked ? [...selected, skill.name] : selected.filter(name => name !== skill.name))} />{skill.name}<small>{skill.description}</small></label>)}
    </details>
    <details className="agent-evidence" open={calls.length > 0}>
      <summary>EXECUTION HISTORY · {calls.length} tool calls</summary>
      {calls.map(call => { const result = results.get(String(call.data.callId)); return <details key={call.seq} className="tool-record">
        <summary>{String(call.data.tool)} · {result ? resultState(result.data.result) : 'AWAITING RESULT'}</summary>
        <pre>{display(call.data.arguments)}</pre>{result && <pre>{display(result.data.result)}</pre>}
      </details> })}
      {events.filter(e => e.kind === 'tool.result' && !calls.some(call => call.data.callId === e.data.callId)).map(e => <details key={e.seq}><summary>{String(e.data.tool)} · {resultState(e.data.result)}</summary><pre>{display(e.data.result)}</pre></details>)}
      {events.filter(e => e.kind.startsWith('control.') || e.kind.startsWith('subagent.') || e.kind === 'transcript.unavailable').map(e => <details key={e.seq}><summary>{e.kind}</summary><pre>{display(e.data)}</pre></details>)}
    </details>
  </section>
}
