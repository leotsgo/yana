// First run, after the owner account exists: start an empty notebook, or
// restore the notes folder from a git backup. The restore runs on the
// server; this screen shows the key to add to the repository, takes the
// address, then polls the job until it is done or has failed.

import type { ComponentChildren } from 'preact'
import { useEffect, useState } from 'preact/hooks'

import { api, ApiError } from './api'
import type { RestoreStatus } from './api'
import { isPathURL, KeyBlock } from './settings'
import { hostOf, splitError } from './setuplib'

type Step = 'loading' | 'choose' | 'key' | 'address' | 'progress'
type Key = { public_key: string; sealed_key: string }

const PHASES: Array<RestoreStatus['phase']> = ['connecting', 'fetching', 'indexing', 'done']
const finished = (s: RestoreStatus | null): boolean => s !== null && (s.phase === 'done' || s.phase === 'failed')

function Card({ children }: { children: ComponentChildren }) {
  return (
    <div class="auth-wrap">
      <div class="auth-form">
        <img class="auth-mark" src="/icon-192.png" alt="" width={72} height={72} />
        <h1 class="wordmark large">YANA/</h1>
        {children}
      </div>
    </div>
  )
}

export function SetupFlow({ onDone }: { onDone: () => void }) {
  const [step, setStep] = useState<Step>('loading')
  const [key, setKey] = useState<Key | null>(null)
  const [url, setUrl] = useState('')
  const [status, setStatus] = useState<RestoreStatus | null>(null)
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)
  const [keyTry, setKeyTry] = useState(0)

  // A restore already running (the page was reloaded) resumes at progress.
  useEffect(() => {
    let live = true
    api
      .setupState()
      .then((s) => {
        if (!live) return
        const r = s.restore
        if (r && !finished(r)) {
          setUrl(r.url)
          setStatus(r)
          setStep('progress')
        } else setStep('choose')
      })
      .catch(() => live && onDone())
    return () => {
      live = false
    }
  }, [])

  const polling = step === 'progress' && !finished(status)
  useEffect(() => {
    if (!polling) return
    // One request at a time: the next poll is scheduled after the last answers.
    let live = true
    let timer: ReturnType<typeof setTimeout>
    const tick = () => {
      timer = setTimeout(() => {
        api
          .restoreStatus()
          .then((s) => live && setStatus(s))
          .catch((err: unknown) => {
            // The server forgot the job (it restarted): start over.
            if (live && err instanceof ApiError && err.status === 404) {
              setMsg('The restore stopped. Try again.')
              setStep('address')
              live = false
            }
          })
          .finally(() => live && tick())
      }, 1000)
    }
    tick()
    return () => {
      live = false
      clearTimeout(timer)
    }
  }, [polling])

  // The key step always has a key to show: made on first sight, and again
  // after a reload lost the one this screen held.
  useEffect(() => {
    if (step !== 'key' || key) return
    let live = true
    setMsg('')
    api
      .newGitKey()
      .then((k) => live && setKey(k))
      .catch((err: unknown) => live && fail(err, 'Could not make a key.'))
    return () => {
      live = false
    }
  }, [step, key, keyTry])

  const fail = (err: unknown, fallback: string) => setMsg(err instanceof ApiError ? err.message : fallback)

  const startEmpty = () => {
    setBusy(true)
    setMsg('')
    api
      .setupEmpty()
      .then(onDone)
      .catch((err: unknown) => {
        fail(err, 'Could not start the notebook.')
        setBusy(false)
      })
  }

  const wantRestore = () => {
    setMsg('')
    setStep('key')
  }

  const restore = () => {
    setBusy(true)
    setMsg('')
    api
      .startRestore(url.trim(), isPathURL(url) ? undefined : key?.sealed_key)
      .then((s) => {
        setStatus(s)
        setStep('progress')
      })
      .catch((err: unknown) => fail(err, 'Could not start the restore.'))
      .finally(() => setBusy(false))
  }

  const back = (to: Step) => () => {
    setMsg('')
    setStep(to)
  }

  const note = (
    <p class="auth-msg" role="status">
      {msg}
    </p>
  )

  switch (step) {
    case 'loading':
      return <div class="boot" aria-busy="true" />
    case 'choose':
      return (
        <Card>
          <p class="auth-sub">Your notes folder is empty. Start fresh, or bring your notes back from a git backup.</p>
          <button class="setup-opt" type="button" disabled={busy} onClick={startEmpty}>
            <strong>Start with an empty notebook</strong>
            <span>You can add a backup remote later in Settings.</span>
          </button>
          <button class="setup-opt" type="button" disabled={busy} onClick={wantRestore}>
            <strong>Restore from a git backup</strong>
            <span>Clone a repository this server pushed to before, then keep backing up to it.</span>
          </button>
          {note}
        </Card>
      )
    case 'key':
      return (
        <Card>
          <p class="auth-sub">This server made a key for your backup repository. Add it there with write access, so the server can read the backup now and push to it later.</p>
          {key ? <KeyBlock publicKey={key.public_key} /> : <span class="muted">{msg ? '' : 'Making a key…'}</span>}
          {!key && msg && (
            <button class="btn" type="button" onClick={() => setKeyTry((n) => n + 1)}>
              Try again
            </button>
          )}
          <ul class="setup-where">
            <li>
              <b>GitHub</b> · repository › Settings › Deploy keys › Add, tick “Allow write access”
            </li>
            <li>
              <b>Gitea / Forgejo / GitLab</b> · repository › Deploy keys, with write access
            </li>
            <li>
              <b>Your own server</b> · a line in the git user’s <code>~/.ssh/authorized_keys</code>
            </li>
          </ul>
          {note}
          <div class="setup-actions">
            <button class="btn" type="button" onClick={back('choose')}>
              ← back
            </button>
            <button class="btn primary" type="button" disabled={!key} onClick={back('address')}>
              I added the key
            </button>
          </div>
        </Card>
      )
    case 'address':
      return (
        <Card>
          <p class="auth-sub">Where is the backup?</p>
          <form
            class="setup-form"
            onSubmit={(ev) => {
              ev.preventDefault()
              restore()
            }}
          >
            <label class="field">
              <span class="field-label">Repository address</span>
              <input
                class="auth-input setup-mono"
                name="url"
                required
                autofocus
                spellcheck={false}
                autocapitalize="off"
                placeholder="git@github.com:you/notes.git"
                value={url}
                onInput={(ev) => setUrl((ev.target as HTMLInputElement).value)}
              />
              <span class="field-hint">An SSH address (git@host:path or ssh://…) or the path of a bare repository on a mounted disk.</span>
            </label>
            {note}
            <div class="setup-actions">
              <button class="btn" type="button" onClick={back('key')}>
                ← back
              </button>
              <button class="btn primary" type="submit" disabled={busy || url.trim() === ''}>
                Connect and restore
              </button>
            </div>
          </form>
        </Card>
      )
    case 'progress':
      return status?.phase === 'failed' ? (
        <Failure status={status} ssh={!isPathURL(url)} onKey={back('key')} onRetry={back(!isPathURL(url) && !key ? 'key' : 'address')} />
      ) : status?.phase === 'done' ? (
        <Card>
          <div class="setup-ok">
            Restored {status.notes === 0 ? 'your notes' : `${status.notes} ${status.notes === 1 ? 'note' : 'notes'}`}. New changes back up to <code>{status.url}</code> nightly at 02:00; change that in Settings › Backup.
          </div>
          <button class="btn primary large" type="button" onClick={onDone}>
            Open notes
          </button>
        </Card>
      ) : (
        <Card>
          <p class="auth-sub">
            Restoring from <code>{url}</code>
          </p>
          <Progress status={status} url={url} />
        </Card>
      )
  }
}

function Progress({ status, url }: { status: RestoreStatus | null; url: string }) {
  const at = Math.max(0, PHASES.indexOf(status?.phase ?? 'connecting'))
  const path = isPathURL(url)
  // Each line is done once the job is past its phase, running while on it.
  const line = (i: number, text: string, extra?: ComponentChildren) => (
    <li class={i < at ? 'done' : i === at ? 'run' : 'todo'}>
      <span class="setup-ic" aria-hidden="true">
        {i < at ? '✓' : i === at ? '◐' : '·'}
      </span>
      <span>
        {text}
        {extra}
      </span>
    </li>
  )
  return (
    <ul class="setup-progress" aria-live="polite">
      {line(0, path ? (at > 0 ? `Opened ${url}` : `Opening ${url}`) : at > 0 ? `Connected to ${hostOf(url)}` : `Connecting to ${hostOf(url)}`, !path && status?.host_key && <small>host key {status.host_key}, kept for next time</small>)}
      {line(1, at > 1 ? `Cloned, ${status?.commits ?? 0} ${status?.commits === 1 ? 'commit' : 'commits'}` : 'Cloning the history')}
      {line(2, at >= 2 && status?.notes ? `Indexing notes · ${status.notes}` : 'Indexing notes')}
      {line(3, 'Keep backing up here, nightly at 02:00')}
    </ul>
  )
}

function Failure({ status, ssh, onKey, onRetry }: { status: RestoreStatus; ssh: boolean; onKey: () => void; onRetry: () => void }) {
  const { head, detail } = splitError(status.error)
  return (
    <Card>
      <p class="auth-sub">
        Restoring from <code>{status.url}</code>
      </p>
      <div class="setup-error" role="alert">
        {head || 'The restore failed.'}
        {detail && <code>{detail}</code>}
      </div>
      <div class="setup-actions">
        {ssh && (
          <button class="btn" type="button" onClick={onKey}>
            Show the key
          </button>
        )}
        <button class="btn primary" type="button" onClick={onRetry}>
          Try again
        </button>
      </div>
    </Card>
  )
}
