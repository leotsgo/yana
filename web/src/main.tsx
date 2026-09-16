// Boot: find out whether the server has accounts, get a session, then
// mount the app. The setup and sign-in screens live here because they run
// before anything else exists. With no network the app mounts on its
// local caches; the session is checked again once the network returns.

import { render } from 'preact'
import { useEffect, useRef, useState } from 'preact/hooks'

import { App } from './app'
import * as auth from './auth'
import { Icon } from './icons'
import * as prefs from './prefs'
import { initPwa } from './pwa'
import './app.css'

prefs.applyTheme()
prefs.applyAppearance()
initPwa()

type Phase = 'loading' | 'setup' | 'signin' | 'app'

function Root() {
  const [phase, setPhase] = useState<Phase>('loading')
  const [epoch, setEpoch] = useState(0)

  useEffect(() => {
    let live = true
    void (async () => {
      let state: { setupRequired: boolean; open: boolean }
      try {
        state = await auth.authState()
      } catch {
        // The shell came from the service worker and the server is
        // unreachable: open on the local copies and sort the session out
        // when the network returns.
        if (live) setPhase('app')
        return
      }
      if (!live) return
      if (state.setupRequired && !state.open) {
        setPhase('setup')
        return
      }
      if (!state.open) {
        let ok = false
        try {
          ok = await auth.tryRefresh()
        } catch {
          ok = false
        }
        if (!live) return
        if (!ok || !auth.token()) {
          setPhase('signin')
          return
        }
      }
      setPhase('app')
    })()
    return () => {
      live = false
    }
  }, [epoch])

  switch (phase) {
    case 'loading':
      return <div class="boot" aria-busy="true" />
    case 'setup':
      return (
        <AuthForm
          title="This server has no account yet. Create the owner account; there are no default credentials."
          passwordLabel="Password (8 characters or more)"
          passwordAutocomplete="new-password"
          button="Create the owner account"
          submit={(u, p) => auth.setup(u, p)}
          onDone={() => setPhase('app')}
        />
      )
    case 'signin':
      return (
        <AuthForm
          title="Sign in to read and edit your notes."
          passwordLabel="Password"
          passwordAutocomplete="current-password"
          button="Sign in"
          submit={(u, p) => auth.login(u, p)}
          onDone={() => setPhase('app')}
        />
      )
    case 'app':
      return <App key={epoch} onSignOut={() => { setPhase('loading'); setEpoch((e) => e + 1) }} />
  }
}

interface AuthFormProps {
  title: string
  passwordLabel: string
  passwordAutocomplete: string
  button: string
  submit: (username: string, password: string) => Promise<void>
  onDone: () => void
}

function AuthForm({ title, passwordLabel, passwordAutocomplete, button, submit, onDone }: AuthFormProps) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)
  const first = useRef<HTMLInputElement>(null)

  useEffect(() => {
    first.current?.focus()
  }, [])

  return (
    <div class="auth-wrap">
      <form
        class="auth-form"
        onSubmit={(ev) => {
          ev.preventDefault()
          setBusy(true)
          setMsg('')
          submit(username, password)
            .then(onDone)
            .catch((err: unknown) => {
              setMsg(err instanceof auth.AuthError ? err.message : 'Something went wrong.')
            })
            .finally(() => setBusy(false))
        }}
      >
        <div class="auth-mark" aria-hidden="true">
          <Icon name="slash" size={40} />
        </div>
        <h1 class="wordmark large">YANA/</h1>
        <p class="auth-sub">{title}</p>
        <label class="field">
          <span class="field-label">Username</span>
          <input
            ref={first}
            class="auth-input"
            name="username"
            autocomplete="username"
            required
            value={username}
            onInput={(ev) => setUsername((ev.target as HTMLInputElement).value)}
          />
        </label>
        <label class="field">
          <span class="field-label">{passwordLabel}</span>
          <input
            class="auth-input"
            name="password"
            type="password"
            autocomplete={passwordAutocomplete}
            required
            value={password}
            onInput={(ev) => setPassword((ev.target as HTMLInputElement).value)}
          />
        </label>
        <p class="auth-msg" role="status">
          {msg}
        </p>
        <button class="btn primary large" type="submit" disabled={busy}>
          {button}
        </button>
      </form>
    </div>
  )
}

const rootNode = document.getElementById('app')
if (!rootNode) throw new Error('missing #app')
render(<Root />, rootNode)
