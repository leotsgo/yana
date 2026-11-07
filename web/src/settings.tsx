// Settings: account, people, spaces and sharing, agents, appearance,
// data. Routes under /settings/*; a column of sections beside the page
// on wide screens, a list that opens one section at a time on a phone.
// Every page is a set of plain forms against endpoints that already
// existed; the preferences pages write through prefs.ts so the shell,
// the editor and the PWA read the same values. A page an account cannot
// use says what it is for and who can, rather than showing nothing.

import type { ComponentChildren } from 'preact'
import { useCallback, useEffect, useMemo, useState } from 'preact/hooks'

import { api, ApiError, saveBlob } from './api'
import type { Account, AgentKey, ConflictEntry, DeletedNote, GitRemote, GitRemoteInput, OrphanAsset, PublicLinkRow, RestorePreview, RemoteSchedule, Role, Session, SpaceDetail, SpaceInfo, Status } from './api'
import * as auth from './auth'
import type { ConfirmSpec } from './confirm'
import { ConflictDialog, fmtAge } from './conflict'
import { dailyPath } from './defaults'
import { fmtBytes, fmtDate, isSet } from './dom'
import { Icon } from './icons'
import type { IconName } from './icons'
import type { Layout } from './layout'
import * as prefs from './prefs'
import { today } from './sharelib'
import { spaceOfDir } from './templates'
import { exportTheme, parseTheme, swatch } from './themes'
import type { Mode, ThemeDef } from './themes'

export type Section = 'account' | 'people' | 'spaces' | 'agents' | 'appearance' | 'data'

export const SECTIONS: Array<{ id: Section; label: string; icon: IconName; blurb: string; owner?: boolean }> = [
  { id: 'account', label: 'Account', icon: 'user', blurb: 'Your name, your password, the devices signed in.' },
  { id: 'people', label: 'People', icon: 'users', blurb: 'The accounts on this server.', owner: true },
  { id: 'spaces', label: 'Spaces and sharing', icon: 'layers', blurb: 'Who can see and edit which top-level folder.' },
  { id: 'agents', label: 'Agents', icon: 'bot', blurb: 'Keys for tools that read and write notes over MCP.', owner: true },
  { id: 'appearance', label: 'Appearance', icon: 'palette', blurb: 'Theme, text size, line width, how notes open.' },
  { id: 'data', label: 'Data', icon: 'database', blurb: 'Exports, the trash, history, and the index.' },
]

export function isSection(s: string): s is Section {
  return SECTIONS.some((x) => x.id === s)
}

export interface SettingsProps {
  /** null shows the list of sections (the phone's index page). */
  section: Section | null
  layout: Layout
  status: Status | null
  spaces: SpaceInfo[] | null
  notes: Array<{ id: string; path: string; title: string }>
  /** Every folder in the tree, by path, spaces included. */
  folders: string[]
  onSection: (s: Section | null) => void
  onToast: (msg: string) => void
  confirm: (spec: ConfirmSpec) => void
  /** Spaces changed; the tree needs a refresh. */
  onChanged: () => void
  onOpen: (id: string) => void
  onOpenTrash: () => void
  onSignOut: () => void
  onStatus: () => void
}

export interface Ctx {
  user: auth.User | null
  status: Status | null
  spaces: SpaceInfo[] | null
  notes: SettingsProps['notes']
  folders: string[]
  say: (msg: string) => void
  confirm: (spec: ConfirmSpec) => void
  onChanged: () => void
  onOpen: (id: string) => void
  onOpenTrash: () => void
  onSignOut: () => void
  onStatus: () => void
}

export function SettingsPage(props: SettingsProps) {
  const { section, layout, onSection } = props
  const user = auth.user()
  const phone = layout === 'phone'
  const shown: Section | null = section ?? (phone ? null : 'account')
  const ctx: Ctx = {
    user,
    status: props.status,
    spaces: props.spaces,
    notes: props.notes,
    folders: props.folders,
    say: props.onToast,
    confirm: props.confirm,
    onChanged: props.onChanged,
    onOpen: props.onOpen,
    onOpenTrash: props.onOpenTrash,
    onSignOut: props.onSignOut,
    onStatus: props.onStatus,
  }

  const nav = (
    <nav class="settings-nav" aria-label="settings sections">
      {SECTIONS.map((s) => (
        <a
          key={s.id}
          class={'settings-link' + (shown === s.id ? ' selected' : '')}
          href={`/settings/${s.id}`}
          aria-current={shown === s.id ? 'page' : undefined}
          onClick={(ev) => {
            ev.preventDefault()
            onSection(s.id)
          }}
        >
          <Icon name={s.icon} size={phone ? 20 : 16} />
          <span class="settings-link-text">
            <span class="settings-link-label">{s.label}</span>
            {phone && <span class="settings-link-blurb">{s.blurb}</span>}
          </span>
          {phone && <Icon name="chevron-right" class="settings-link-caret" />}
        </a>
      ))}
    </nav>
  )

  if (phone && shown === null) {
    return (
      <div class="page-scroll settings settings-index">
        <header class="report-head">
          <h1 class="report-title">Settings</h1>
        </header>
        {nav}
      </div>
    )
  }

  const meta = SECTIONS.find((s) => s.id === shown)
  const body = shown && <SectionBody section={shown} ctx={ctx} />

  if (phone) {
    return (
      <div class="page-scroll settings settings-one">
        <header class="report-head">
          <button type="button" class="icon-btn" aria-label="All settings" onClick={() => onSection(null)}>
            <Icon name="arrow-left" size={18} />
          </button>
          <h1 class="report-title">{meta?.label}</h1>
        </header>
        <div class="settings-main">{body}</div>
      </div>
    )
  }

  return (
    <div class="page-scroll settings">
      <div class="settings-cols">
        <div class="settings-side">
          <h1 class="report-title settings-title">Settings</h1>
          {nav}
        </div>
        <div class="settings-main">
          <h2 class="settings-heading">{meta?.label}</h2>
          {body}
        </div>
      </div>
    </div>
  )
}

function SectionBody({ section, ctx }: { section: Section; ctx: Ctx }) {
  switch (section) {
    case 'account':
      return <AccountSection ctx={ctx} />
    case 'people':
      return <PeopleSection ctx={ctx} />
    case 'spaces':
      return <SpacesSection ctx={ctx} />
    case 'agents':
      return <AgentsSection ctx={ctx} />
    case 'appearance':
      return <AppearanceSection ctx={ctx} />
    case 'data':
      return <DataSection ctx={ctx} />
  }
}

// --- shared pieces ---------------------------------------------------------

function msgOf(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback
}

/** A titled block of one page. */
function Block({ title, lead, children }: { title: string; lead?: string; children: ComponentChildren }) {
  return (
    <section class="settings-block">
      <h3 class="settings-block-title">{title}</h3>
      {lead && <p class="settings-lead">{lead}</p>}
      {children}
    </section>
  )
}

/** What a page is for, and why this account cannot use it. */
function CannotUse({ what, who }: { what: string; who: string }) {
  return (
    <div class="settings-locked" role="status">
      <Icon name="shield" size={20} />
      <div>
        <p>{what}</p>
        <p class="muted">{who}</p>
      </div>
    </div>
  )
}

function NoAccounts({ what }: { what: string }) {
  return (
    <CannotUse
      what={what}
      who="This server runs without accounts: everyone who can reach it reads and edits everything, and there is nothing here to manage."
    />
  )
}

interface ChoiceProps<T extends string> {
  label: string
  value: T
  options: Array<{ value: T; label: string; hint?: string }>
  onChange: (v: T) => void
}

/** One row of a preferences page: a label and a segmented control. */
function Choice<T extends string>({ label, value, options, onChange }: ChoiceProps<T>) {
  return (
    <div class="pref-row">
      <span class="pref-label">{label}</span>
      <div class="segmented" role="radiogroup" aria-label={label}>
        {options.map((o) => (
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={value === o.value}
            class={value === o.value ? 'on' : ''}
            title={o.hint}
            onClick={() => onChange(o.value)}
          >
            {o.label}
          </button>
        ))}
      </div>
    </div>
  )
}

/** A folder of one space, its top first. A stored folder the tree no
 * longer has stays on offer; the next note made there makes it again. */
function FolderSelect({ id, space, value, folders, none, disabled, onChange }: { id: string; space: string; value: string; folders: string[]; none: string; disabled?: boolean; onChange: (v: string) => void }) {
  const inside = folders.filter((f) => f.startsWith(space + '/'))
  const gone = value !== '' && value !== space && !inside.includes(value)
  return (
    <select id={id} class="select" value={value} disabled={disabled} onChange={(ev) => onChange((ev.target as HTMLSelectElement).value)}>
      <option value="">{none}</option>
      <option value={space}>the top of {space}</option>
      {inside.map((f) => (
        <option key={f} value={f}>
          {f.slice(space.length + 1)}
        </option>
      ))}
      {gone && <option value={value}>{value.slice(space.length + 1)} (made on first use)</option>}
    </select>
  )
}

function Toggle({ label, hint, on, onChange }: { label: string; hint?: string; on: boolean; onChange: (v: boolean) => void }) {
  return (
    <label class="pref-row pref-toggle">
      <span class="pref-label">
        {label}
        {hint && <span class="pref-hint">{hint}</span>}
      </span>
      <input type="checkbox" class="switch" checked={on} onChange={(ev) => onChange((ev.target as HTMLInputElement).checked)} />
    </label>
  )
}

function SpaceSelect({ value, spaces, any, disabled, onChange, id }: { id?: string; value: string; spaces: SpaceInfo[]; any: string; disabled?: boolean; onChange: (v: string) => void }) {
  return (
    <select id={id} class="select" value={value} disabled={disabled} onChange={(ev) => onChange((ev.target as HTMLSelectElement).value)}>
      <option value="">{any}</option>
      {value !== '' && !spaces.some((s) => s.name === value) && <option value={value}>{value}</option>}
      {spaces.map((s) => (
        <option key={s.name} value={s.name}>
          {s.name === '' ? 'the root' : s.label || s.name}
        </option>
      ))}
    </select>
  )
}

async function copyText(text: string, say: (m: string) => void, what: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text)
    say(`Copied ${what}.`)
  } catch {
    say('The browser refused the clipboard. Select the text and copy it.')
  }
}

// --- account -----------------------------------------------------------------

function AccountSection({ ctx }: { ctx: Ctx }) {
  const { user, say } = ctx
  const [name, setName] = useState(prefs.displayName)
  const [saved, setSaved] = useState(false)

  const submitName = (ev: Event) => {
    ev.preventDefault()
    prefs.setDisplayName(name)
    setName(prefs.displayName())
    setSaved(true)
    say(prefs.displayName() ? `You show up as ${prefs.displayName()}.` : 'Display name cleared.')
  }

  return (
    <>
      {user ? (
        <p class="settings-lead">
          Signed in as <strong>{user.username}</strong>
          {user.is_owner && <span class="badge">owner</span>}
        </p>
      ) : (
        <NoAccounts what="Account settings hold a password and the devices signed in with it." />
      )}
      <Block
        title="Display name"
        lead={
          user
            ? 'Shown beside your cursor to people editing with you. Empty uses your username.'
            : 'Shown beside your cursor to people editing with you, and the author of your edits in the history. Empty uses a generated name.'
        }
      >
        <form class="settings-form inline" onSubmit={submitName}>
          <input
            class="input"
            type="text"
            maxLength={40}
            autocomplete="nickname"
            placeholder={user ? user.username : 'a name'}
            value={name}
            aria-label="Display name"
            onInput={(ev) => {
              setName((ev.target as HTMLInputElement).value)
              setSaved(false)
            }}
          />
          <button type="submit" class="btn" disabled={saved && name === prefs.displayName()}>
            Save
          </button>
        </form>
      </Block>
      {user && <PasswordBlock ctx={ctx} />}
      {user && <SessionsBlock ctx={ctx} />}
    </>
  )
}

function PasswordBlock({ ctx }: { ctx: Ctx }) {
  const { user, say } = ctx
  const [pw, setPw] = useState('')
  const [again, setAgain] = useState('')
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')
  if (!user) return null

  const submit = (ev: Event) => {
    ev.preventDefault()
    if (pw !== again) {
      setMsg('The two passwords differ.')
      return
    }
    setBusy(true)
    setMsg('')
    api
      .setPassword(user.id, pw)
      .then(() => {
        setPw('')
        setAgain('')
        say('Password changed. Every other device is signed out.')
      })
      .catch((err: unknown) => setMsg(msgOf(err, 'Could not change the password.')))
      .finally(() => setBusy(false))
  }

  return (
    <Block title="Password" lead="At least 8 characters. Changing it signs out every other device; this one stays.">
      <form class="settings-form" onSubmit={submit}>
        <input class="input" type="text" name="username" autocomplete="username" value={user.username} hidden readOnly />
        <label class="field">
          <span class="field-label">New password</span>
          <input class="input" type="password" autocomplete="new-password" required minLength={8} value={pw} onInput={(ev) => setPw((ev.target as HTMLInputElement).value)} />
        </label>
        <label class="field">
          <span class="field-label">Again</span>
          <input class="input" type="password" autocomplete="new-password" required minLength={8} value={again} onInput={(ev) => setAgain((ev.target as HTMLInputElement).value)} />
        </label>
        <p class="form-msg" role="status">
          {msg}
        </p>
        <div class="form-actions">
          <button type="submit" class="btn primary" disabled={busy || pw.length < 8}>
            Change password
          </button>
        </div>
      </form>
    </Block>
  )
}

function SessionsBlock({ ctx }: { ctx: Ctx }) {
  const { say, onSignOut, confirm } = ctx
  const [sessions, setSessions] = useState<Session[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  const reload = useCallback(async () => {
    try {
      const { sessions } = await api.sessions()
      const now = Date.now()
      setSessions(sessions.filter((s) => !s.revoked_at && new Date(s.expires_at).getTime() > now))
      setError(null)
    } catch (err) {
      setError(msgOf(err, 'Could not list the sessions.'))
    }
  }, [])

  useEffect(() => {
    void reload()
  }, [reload])

  const revoke = (s: Session) => {
    setBusy(s.id)
    api
      .revokeSession(s.id)
      .then(() => {
        say(s.current ? 'Signed out.' : `Signed out ${s.label}.`)
        if (s.current) onSignOut()
        else void reload()
      })
      .catch((err: unknown) => say(msgOf(err, 'Could not sign that device out.')))
      .finally(() => setBusy(null))
  }

  const revokeOthers = () => {
    const others = (sessions ?? []).filter((s) => !s.current)
    if (others.length === 0) return
    confirm({
      title: `Sign out ${others.length} other ${others.length === 1 ? 'device' : 'devices'}?`,
      body: 'Each one is back at the sign-in screen on its next request. This device stays signed in.',
      rows: others.map((s) => ({ label: s.label, detail: `last used ${fmtDate(s.last_used_at)}` })),
      confirmLabel: 'Sign them out',
      onConfirm: () => {
        setBusy('all')
        Promise.allSettled(others.map((s) => api.revokeSession(s.id)))
          .then((results) => {
            const failed = results.filter((r) => r.status === 'rejected').length
            say(failed ? `${failed} could not be signed out.` : 'Every other device is signed out.')
            void reload()
          })
          .finally(() => setBusy(null))
      },
    })
  }

  return (
    <Block title="Devices" lead="Every sign-in is a session with the device that opened it. Signing one out takes effect on its next request.">
      {error ? (
        <p class="error">{error}</p>
      ) : !sessions ? (
        <p class="muted">Loading…</p>
      ) : (
        <>
          <ul class="settings-list">
            {sessions.map((s) => (
              <li key={s.id} class="settings-row">
                <Icon name={/android|ios|iphone|ipad/i.test(s.label) ? 'smartphone' : 'monitor'} class="settings-row-icon" />
                <div class="settings-row-main">
                  <span class="settings-row-title">
                    {s.label}
                    {s.current && <span class="badge">this device</span>}
                  </span>
                  <span class="settings-row-sub">
                    signed in {fmtDate(s.created_at)} · last used {fmtDate(s.last_used_at)}
                  </span>
                </div>
                <button type="button" class="btn small" disabled={busy !== null} onClick={() => revoke(s)}>
                  <Icon name="log-out" />
                  Sign out
                </button>
              </li>
            ))}
          </ul>
          {sessions.filter((s) => !s.current).length > 0 && (
            <div class="form-actions">
              <button type="button" class="btn" disabled={busy !== null} onClick={revokeOthers}>
                Sign out every other device
              </button>
            </div>
          )}
        </>
      )}
    </Block>
  )
}

// --- people ------------------------------------------------------------------

function PeopleSection({ ctx }: { ctx: Ctx }) {
  const { user, say, confirm } = ctx
  const [users, setUsers] = useState<Account[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [msg, setMsg] = useState('')
  const [resetting, setResetting] = useState<string | null>(null)
  const [resetPw, setResetPw] = useState('')
  const owner = user?.is_owner === true

  const reload = useCallback(async () => {
    try {
      const { users } = await api.users()
      setUsers(users)
      setError(null)
    } catch (err) {
      setError(msgOf(err, 'Could not list the accounts.'))
    }
  }, [])

  useEffect(() => {
    if (owner) void reload()
  }, [owner, reload])

  if (!user) return <NoAccounts what="People lists the accounts on this server and lets the owner add, remove, and reset them." />
  if (!owner) {
    return (
      <CannotUse
        what="People lists the accounts on this server: who can sign in, and the owner adds, removes, and resets them."
        who={`Your account (${user.username}) is not the owner, so this page is read-only for you and shows nothing to change. The owner can add someone or reset a password.`}
      />
    )
  }

  const add = (ev: Event) => {
    ev.preventDefault()
    setBusy(true)
    setMsg('')
    api
      .createUser(username.trim(), password)
      .then((u) => {
        say(`Added ${u.username}. Give them the password; they can change it once signed in.`)
        setUsername('')
        setPassword('')
        void reload()
      })
      .catch((err: unknown) => setMsg(msgOf(err, 'Could not add the account.')))
      .finally(() => setBusy(false))
  }

  const remove = (u: Account) => {
    confirm({
      title: `Remove ${u.username}?`,
      body: 'Their sessions end now and they cannot sign in again. Notes they wrote stay where they are; their name stays in the history.',
      confirmLabel: 'Remove account',
      danger: true,
      onConfirm: () => {
        api
          .deleteUser(u.id)
          .then(() => {
            say(`Removed ${u.username}.`)
            void reload()
          })
          .catch((err: unknown) => say(msgOf(err, 'Could not remove the account.')))
      },
    })
  }

  const reset = (u: Account, ev: Event) => {
    ev.preventDefault()
    setBusy(true)
    api
      .setPassword(u.id, resetPw)
      .then(() => {
        say(`Password reset for ${u.username}. Their other devices are signed out.`)
        setResetting(null)
        setResetPw('')
      })
      .catch((err: unknown) => say(msgOf(err, 'Could not reset the password.')))
      .finally(() => setBusy(false))
  }

  return (
    <>
      <Block title="Accounts" lead="Everyone who can sign in. Access to notes is set per space, on the Spaces page.">
        {error ? (
          <p class="error">{error}</p>
        ) : !users ? (
          <p class="muted">Loading…</p>
        ) : (
          <ul class="settings-list">
            {users.map((u) => (
              <li key={u.id} class="settings-row">
                <span class="avatar small">{u.username.slice(0, 1).toUpperCase()}</span>
                <div class="settings-row-main">
                  <span class="settings-row-title">
                    {u.username}
                    {u.is_owner && <span class="badge">owner</span>}
                    {u.id === user.id && <span class="badge">you</span>}
                  </span>
                  <span class="settings-row-sub">since {fmtDate(u.created_at)}</span>
                  {resetting === u.id && (
                    <form class="settings-form inline" onSubmit={(ev) => reset(u, ev)}>
                      <input
                        class="input"
                        type="password"
                        autocomplete="new-password"
                        placeholder="New password (8 characters or more)"
                        minLength={8}
                        required
                        aria-label={`New password for ${u.username}`}
                        value={resetPw}
                        onInput={(ev) => setResetPw((ev.target as HTMLInputElement).value)}
                      />
                      <button type="submit" class="btn small primary" disabled={busy || resetPw.length < 8}>
                        Set
                      </button>
                      <button type="button" class="btn small" onClick={() => setResetting(null)}>
                        Cancel
                      </button>
                    </form>
                  )}
                </div>
                {u.id !== user.id && (
                  <div class="settings-row-actions">
                    <button type="button" class="btn small" onClick={() => { setResetting(resetting === u.id ? null : u.id); setResetPw('') }}>
                      <Icon name="key" />
                      Reset password
                    </button>
                    <button type="button" class="btn small danger" onClick={() => remove(u)}>
                      <Icon name="x" />
                      Remove
                    </button>
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}
      </Block>
      <Block title="Add an account" lead="A username of 2 to 32 letters, digits, dots, underscores or dashes, and a starting password they can change.">
        <form class="settings-form" onSubmit={add}>
          <label class="field">
            <span class="field-label">Username</span>
            <input class="input" type="text" autocomplete="off" required minLength={2} maxLength={32} value={username} onInput={(ev) => setUsername((ev.target as HTMLInputElement).value)} />
          </label>
          <label class="field">
            <span class="field-label">Password</span>
            <input class="input" type="password" autocomplete="new-password" required minLength={8} value={password} onInput={(ev) => setPassword((ev.target as HTMLInputElement).value)} />
          </label>
          <p class="form-msg" role="status">
            {msg}
          </p>
          <div class="form-actions">
            <button type="submit" class="btn primary" disabled={busy || username.trim().length < 2 || password.length < 8}>
              <Icon name="plus" />
              Add account
            </button>
          </div>
        </form>
      </Block>
    </>
  )
}

// --- spaces ------------------------------------------------------------------

function SpacesSection({ ctx }: { ctx: Ctx }) {
  const { user, say, spaces, onChanged } = ctx
  const [list, setList] = useState<SpaceInfo[] | null>(spaces)
  const [error, setError] = useState<string | null>(null)
  const [open, setOpen] = useState<string | null>(null)
  const [newName, setNewName] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)
  const [start, setStart] = useState(prefs.newNoteStart)
  // The defaults live in .yana/defaults.json; this follows the copy the
  // shell fetched, and only the owner changes it.
  usePrefs()
  const owner = auth.user()?.is_owner ?? true
  const { space: defSpace, folder: newFolder, daily_space: dailySpace, daily_folder: dailyFolder } = prefs.defaults()
  const saveDefaults = (patch: Partial<prefs.Defaults>) => {
    const prev = prefs.defaults()
    const next = { ...prev, ...patch }
    // A folder belongs to one space; picking another space lets it go.
    if (next.folder && spaceOfDir(next.folder) !== next.space) next.folder = ''
    if (next.daily_folder && spaceOfDir(next.daily_folder) !== (next.daily_space || next.space)) next.daily_folder = ''
    prefs.setDefaults(next)
    api.saveDefaults(next).catch((err: unknown) => {
      prefs.setDefaults(prev)
      say(msgOf(err, 'Could not save the defaults.'))
    })
  }
  // The space the daily folder is picked in: its own, else new notes'.
  const dailyIn = dailySpace || defSpace
  const pattern = ctx.status?.daily.pattern

  const reload = useCallback(async () => {
    try {
      const { spaces } = await api.spaces()
      setList(spaces)
      setError(null)
    } catch (err) {
      setError(msgOf(err, 'Could not list the spaces.'))
    }
  }, [])

  useEffect(() => {
    void reload()
  }, [reload])

  const create = (ev: Event) => {
    ev.preventDefault()
    setBusy(true)
    setMsg('')
    api
      .createSpace(newName.trim())
      .then(({ name }) => {
        say(`Created the space ${name}.`)
        setNewName('')
        setOpen(name)
        onChanged()
        void reload()
      })
      .catch((err: unknown) => setMsg(msgOf(err, 'Could not create the space.')))
      .finally(() => setBusy(false))
  }

  const named = (list ?? []).filter((s) => s.name !== '')

  return (
    <>
      <Block
        title="Spaces"
        lead={
          user
            ? 'A space is a top-level folder with a member list in its .space.yml. Viewers read, editors write, owners share. Whoever is not a member cannot tell the space exists.'
            : 'A space is a top-level folder. With accounts on, each one carries a member list; without them, everyone sees every space.'
        }
      >
        {error ? (
          <p class="error">{error}</p>
        ) : !list ? (
          <p class="muted">Loading…</p>
        ) : named.length === 0 ? (
          <p class="muted">No spaces yet. Notes loose in the root are only the owner's; create a space to share anything.</p>
        ) : (
          <ul class="settings-list">
            {named.map((s) => (
              <li key={s.name} class={'settings-row space-row' + (open === s.name ? ' open' : '')}>
                <button type="button" class="space-head" aria-expanded={open === s.name} onClick={() => setOpen(open === s.name ? null : s.name)}>
                  <Icon name={open === s.name ? 'chevron-down' : 'chevron-right'} class="settings-row-icon" />
                  <div class="settings-row-main">
                    <span class="settings-row-title">{s.label || s.name}</span>
                    <span class="settings-row-sub mono">
                      {s.name}/ · {s.notes} {s.notes === 1 ? 'note' : 'notes'}
                    </span>
                  </div>
                </button>
                {open === s.name && (
                  <SpaceDetailView
                    ctx={ctx}
                    space={s}
                    onGone={() => {
                      setOpen(null)
                      void reload()
                      onChanged()
                    }}
                    onRenamed={() => void reload()}
                  />
                )}
              </li>
            ))}
          </ul>
        )}
      </Block>
      <Block title="New space" lead="One directory name: letters, digits, dash, underscore. You are its owner.">
        <form class="settings-form inline" onSubmit={create}>
          <input
            class="input mono"
            type="text"
            pattern="[A-Za-z0-9_\\-]+"
            required
            placeholder="projects"
            aria-label="Space name"
            value={newName}
            onInput={(ev) => setNewName((ev.target as HTMLInputElement).value)}
          />
          <button type="submit" class="btn primary" disabled={busy || newName.trim() === ''}>
            <Icon name="plus" />
            Create
          </button>
        </form>
        {msg && <p class="form-msg">{msg}</p>}
      </Block>
      <Block
        title="Defaults"
        lead={
          'Where new notes and the daily note go, on every device and for every account. Kept with the notes in .yana/defaults.json.' +
          (owner ? '' : ' The owner account sets them.')
        }
      >
        <div class="pref-row">
          <label class="pref-label" for="pref-space">
            Space for new notes
          </label>
          <SpaceSelect
            id="pref-space"
            value={defSpace}
            spaces={list ?? []}
            any="the open note's space, else the first"
            disabled={!owner}
            onChange={(v) => saveDefaults({ space: v })}
          />
        </div>
        {defSpace && (
          <div class="pref-row">
            <label class="pref-label" for="pref-folder">
              Folder for new notes
              <span class="pref-hint">New, the palette and new links put a note here. The tree's + and New note here keep their folder.</span>
            </label>
            <FolderSelect
              id="pref-folder"
              space={defSpace}
              value={newFolder}
              folders={ctx.folders}
              none="none, see where new notes start"
              disabled={!owner}
              onChange={(v) => saveDefaults({ folder: v })}
            />
          </div>
        )}
        {!newFolder && (
          <div class="pref-row">
            <label class="pref-label" for="pref-start">
              New notes start
            </label>
            <select
              id="pref-start"
              class="select"
              value={start}
              onChange={(ev) => {
                const v = (ev.target as HTMLSelectElement).value === 'last' ? 'last' : 'beside'
                prefs.setNewNoteStart(v)
                setStart(v)
              }}
            >
              <option value="beside">beside the open note, else the last folder</option>
              <option value="last">always in the last folder</option>
            </select>
          </div>
        )}
        <div class="pref-row">
          <label class="pref-label" for="pref-daily">
            Daily note
          </label>
          <SpaceSelect
            id="pref-daily"
            value={dailySpace}
            spaces={list ?? []}
            any="same as new notes"
            disabled={!owner}
            onChange={(v) => saveDefaults({ daily_space: v })}
          />
        </div>
        {dailyIn && (
          <div class="pref-row">
            <label class="pref-label" for="pref-daily-folder">
              Folder for the daily note
              {pattern && (
                <span class="pref-hint">
                  Today: {dailyIn}/{dailyPath(pattern, dailyFolder ? dailyFolder.slice(dailyIn.length + 1) : undefined, today())}
                </span>
              )}
            </label>
            <FolderSelect
              id="pref-daily-folder"
              space={dailyIn}
              value={dailyFolder}
              folders={ctx.folders}
              none="as the server's pattern says"
              disabled={!owner}
              onChange={(v) => saveDefaults({ daily_folder: v })}
            />
          </div>
        )}
      </Block>
    </>
  )
}

/** "a viewer", "an editor", "an owner". */
function aRole(r: Role): string {
  return (r === 'viewer' ? 'a ' : 'an ') + r
}

const ROLES: Array<{ value: Role; label: string; hint: string }> = [
  { value: 'viewer', label: 'Viewer', hint: 'reads and searches; sees who is editing' },
  { value: 'editor', label: 'Editor', hint: 'also creates, edits and moves notes' },
  { value: 'owner', label: 'Owner', hint: 'also changes the member list and removes the space' },
]

function SpaceDetailView({ ctx, space, onGone, onRenamed }: { ctx: Ctx; space: SpaceInfo; onGone: () => void; onRenamed: () => void }) {
  const { user, say, confirm } = ctx
  const [detail, setDetail] = useState<SpaceDetail | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [label, setLabel] = useState('')
  const [addUser, setAddUser] = useState('')
  const [addRole, setAddRole] = useState<Role>('editor')
  const [busy, setBusy] = useState(false)
  const [names, setNames] = useState<string[]>([])

  const reload = useCallback(async () => {
    try {
      const d = await api.space(space.name)
      setDetail(d)
      setLabel(d.label)
      setError(null)
    } catch (err) {
      setError(msgOf(err, 'Could not read the space.'))
    }
  }, [space.name])

  useEffect(() => {
    void reload()
  }, [reload])

  // The global owner gets the account list to pick from; a space owner
  // who is not types a username.
  useEffect(() => {
    if (!user?.is_owner) return
    api
      .users()
      .then(({ users }) => setNames(users.map((u) => u.username)))
      .catch(() => setNames([]))
  }, [user?.is_owner])

  const save = (next: SpaceDetail['members'], nextLabel: string, done: string) => {
    setBusy(true)
    api
      .updateSpace(space.name, nextLabel, (next ?? []).map((m) => ({ user: m.username || m.user, role: m.role })))
      .then(() => {
        say(done)
        void reload()
        onRenamed()
      })
      .catch((err: unknown) => say(msgOf(err, 'Could not change the space.')))
      .finally(() => setBusy(false))
  }

  if (error) return <p class="error space-detail">{error}</p>
  if (!detail) return <p class="muted space-detail">Loading…</p>

  const role = detail.role
  const members = detail.members ?? []

  if (role !== 'owner') {
    return (
      <div class="space-detail">
        <p class="settings-lead">
          You are {aRole(role)} here: {role === 'editor' ? 'you create, edit and move notes in it' : 'you read and search it'}. The member
          list is the space owner's to change.
        </p>
      </div>
    )
  }

  const addMember = (ev: Event) => {
    ev.preventDefault()
    const name = addUser.trim().toLowerCase()
    if (!name) return
    if (members.some((m) => (m.username || m.user).toLowerCase() === name)) {
      say(`${name} is already a member.`)
      return
    }
    save([...members, { user: name, role: addRole }], detail.label, `Added ${name} as ${aRole(addRole)}.`)
    setAddUser('')
  }

  const removeSpace = () => {
    confirm({
      title: `Remove the space ${space.name}?`,
      body:
        space.notes > 0
          ? `It still holds ${space.notes} ${space.notes === 1 ? 'note' : 'notes'}. Move or delete them first; a space with anything in it is not removed.`
          : 'The directory and its .space.yml go. Nothing else is in it.',
      confirmLabel: 'Remove space',
      danger: true,
      onConfirm: () => {
        api
          .deleteSpace(space.name)
          .then(() => {
            say(`Removed ${space.name}.`)
            onGone()
          })
          .catch((err: unknown) => say(msgOf(err, 'Could not remove the space.')))
      },
    })
  }

  return (
    <div class="space-detail">
      <form
        class="settings-form inline"
        onSubmit={(ev) => {
          ev.preventDefault()
          save(members, label.trim() || space.name, `Renamed to ${label.trim() || space.name}.`)
        }}
      >
        <label class="field grow">
          <span class="field-label">Display name</span>
          <input class="input" type="text" value={label} aria-label="Display name" onInput={(ev) => setLabel((ev.target as HTMLInputElement).value)} />
        </label>
        <button type="submit" class="btn" disabled={busy || label.trim() === detail.label}>
          Rename
        </button>
      </form>
      <h4 class="settings-sub">Members</h4>
      {user?.is_owner && (
        <p class="muted small">You are the owner account, a member of every space without being listed.</p>
      )}
      {members.length === 0 ? (
        <p class="muted small">Nobody but the owner. Add someone below.</p>
      ) : (
        <ul class="settings-list members">
          {members.map((m) => {
            const self = user !== null && m.id === user.id
            return (
              <li key={m.user} class="settings-row">
                <span class="avatar small">{(m.username || m.user).slice(0, 1).toUpperCase()}</span>
                <div class="settings-row-main">
                  <span class="settings-row-title">
                    {m.username || m.user}
                    {self && <span class="badge">you</span>}
                    {!m.id && user && <span class="badge warn" title="No account has this name; the line stays in .space.yml and applies once one exists">no account</span>}
                  </span>
                </div>
                <select
                  class="select small"
                  aria-label={`Role of ${m.username || m.user}`}
                  value={m.role}
                  disabled={busy || self}
                  onChange={(ev) => {
                    const r = (ev.target as HTMLSelectElement).value as Role
                    save(members.map((x) => (x === m ? { ...x, role: r } : x)), detail.label, `${m.username || m.user} is now ${aRole(r)}.`)
                  }}
                >
                  {ROLES.map((r) => (
                    <option key={r.value} value={r.value}>
                      {r.label}
                    </option>
                  ))}
                </select>
                <button
                  type="button"
                  class="icon-btn"
                  title={self ? 'You cannot remove yourself' : `Remove ${m.username || m.user}`}
                  aria-label={`Remove ${m.username || m.user}`}
                  disabled={busy || self}
                  onClick={() => save(members.filter((x) => x !== m), detail.label, `Removed ${m.username || m.user}.`)}
                >
                  <Icon name="x" />
                </button>
              </li>
            )
          })}
        </ul>
      )}
      <form class="settings-form inline" onSubmit={addMember}>
        <input
          class="input"
          type="text"
          list={names.length > 0 ? `names-${space.name}` : undefined}
          autocomplete="off"
          placeholder="username"
          aria-label="Username to add"
          value={addUser}
          onInput={(ev) => setAddUser((ev.target as HTMLInputElement).value)}
        />
        {names.length > 0 && (
          <datalist id={`names-${space.name}`}>
            {names.filter((n) => !members.some((m) => m.username === n)).map((n) => (
              <option key={n} value={n} />
            ))}
          </datalist>
        )}
        <select class="select" aria-label="Role" value={addRole} onChange={(ev) => setAddRole((ev.target as HTMLSelectElement).value as Role)}>
          {ROLES.map((r) => (
            <option key={r.value} value={r.value} title={r.hint}>
              {r.label}
            </option>
          ))}
        </select>
        <button type="submit" class="btn" disabled={busy || addUser.trim() === ''}>
          <Icon name="plus" />
          Add
        </button>
      </form>
      <p class="muted small">{ROLES.map((r) => `${r.label}: ${r.hint}.`).join(' ')}</p>
      <div class="form-actions">
        <button type="button" class="btn danger" disabled={busy} onClick={removeSpace}>
          <Icon name="trash" />
          Remove space
        </button>
      </div>
    </div>
  )
}

// --- agents ------------------------------------------------------------------

function AgentsSection({ ctx }: { ctx: Ctx }) {
  const { user, say, confirm, spaces } = ctx
  const [keys, setKeys] = useState<AgentKey[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [label, setLabel] = useState('')
  const [scope, setScope] = useState<string[]>([])
  const [canWrite, setCanWrite] = useState(true)
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')
  const [minted, setMinted] = useState<{ label: string; token: string } | null>(null)
  const owner = user?.is_owner === true
  const mcpURL = `${location.origin}/mcp`

  const reload = useCallback(async () => {
    try {
      const { agents } = await api.agents()
      setKeys(agents)
      setError(null)
    } catch (err) {
      setError(msgOf(err, 'Could not list the agent keys.'))
    }
  }, [])

  useEffect(() => {
    if (owner) void reload()
  }, [owner, reload])

  if (!user) return <NoAccounts what="Agents holds the keys tools present at the MCP endpoint to read and write notes." />
  if (!owner) {
    return (
      <CannotUse
        what="Agents holds the keys tools present at the MCP endpoint to read and write notes: each one scoped to spaces, read-only or not, revocable."
        who={`Your account (${user.username}) is not the owner. A key is a grant of access, so only the owner mints or revokes one.`}
      />
    )
  }

  const create = (ev: Event) => {
    ev.preventDefault()
    setBusy(true)
    setMsg('')
    api
      .createAgent(label.trim(), scope, canWrite)
      .then((k) => {
        setMinted({ label: k.label, token: k.token })
        setLabel('')
        setScope([])
        void reload()
      })
      .catch((err: unknown) => setMsg(msgOf(err, 'Could not create the key.')))
      .finally(() => setBusy(false))
  }

  const revoke = (k: AgentKey) => {
    confirm({
      title: `Revoke ${k.label}?`,
      body: 'The next request with this key fails. Notes it wrote stay, and its label stays in the history.',
      confirmLabel: 'Revoke',
      danger: true,
      onConfirm: () => {
        api
          .revokeAgent(k.id)
          .then(() => {
            say(`Revoked ${k.label}.`)
            void reload()
          })
          .catch((err: unknown) => say(msgOf(err, 'Could not revoke the key.')))
      },
    })
  }

  const named = (spaces ?? []).filter((s) => s.name !== '')
  const live = (keys ?? []).filter((k) => !k.revoked_at)
  const gone = (keys ?? []).filter((k) => k.revoked_at)
  const snippet = (token: string) =>
    JSON.stringify({ mcpServers: { yana: { url: mcpURL, headers: { Authorization: `Bearer ${token}` } } } }, null, 2)

  return (
    <>
      <Block title="Endpoint" lead="Point an MCP client here with a key as the bearer token. Every write it makes is authored by the key's label, live for open clients, and committed to history under it.">
        <div class="copy-row">
          <code class="copy-value">{mcpURL}</code>
          <button type="button" class="btn small" onClick={() => void copyText(mcpURL, say, 'the MCP URL')}>
            <Icon name="copy" />
            Copy
          </button>
        </div>
      </Block>
      {minted && (
        <Block title={`Key for ${minted.label}`} lead="This is the only time the secret is shown. Copy it into the client's configuration now.">
          <div class="copy-row">
            <code class="copy-value secret">{minted.token}</code>
            <button type="button" class="btn small primary" onClick={() => void copyText(minted.token, say, 'the key')}>
              <Icon name="copy" />
              Copy key
            </button>
          </div>
          <pre class="note-source snippet">{snippet(minted.token)}</pre>
          <div class="form-actions">
            <button type="button" class="btn small" onClick={() => void copyText(snippet(minted.token), say, 'the configuration')}>
              <Icon name="copy" />
              Copy configuration
            </button>
            <button type="button" class="btn small" onClick={() => setMinted(null)}>
              Done
            </button>
          </div>
        </Block>
      )}
      <Block title="Keys">
        {error ? (
          <p class="error">{error}</p>
        ) : !keys ? (
          <p class="muted">Loading…</p>
        ) : live.length === 0 ? (
          <p class="muted">No keys yet. Create one below.</p>
        ) : (
          <ul class="settings-list">
            {live.map((k) => (
              <li key={k.id} class="settings-row">
                <Icon name="bot" class="settings-row-icon" />
                <div class="settings-row-main">
                  <span class="settings-row-title">
                    {k.label}
                    <span class="badge">{k.can_write ? 'read and write' : 'read only'}</span>
                  </span>
                  <span class="settings-row-sub">
                    {k.spaces.length > 0 ? k.spaces.join(', ') : 'no spaces'} · created {fmtDate(k.created_at)} ·{' '}
                    {new Date(k.last_used_at).getTime() > new Date(k.created_at).getTime() ? `last used ${fmtDate(k.last_used_at)}` : 'never used'}
                  </span>
                </div>
                <button type="button" class="btn small danger" onClick={() => revoke(k)}>
                  <Icon name="x" />
                  Revoke
                </button>
              </li>
            ))}
          </ul>
        )}
        {gone.length > 0 && (
          <p class="muted small">
            {gone.length} revoked: {gone.map((k) => k.label).join(', ')}.
          </p>
        )}
      </Block>
      <Block title="New key" lead="A label names the agent in the history and in git. It sees only the spaces ticked here.">
        <form class="settings-form" onSubmit={create}>
          <label class="field">
            <span class="field-label">Label</span>
            <input class="input" type="text" required maxLength={64} placeholder="homelab-docs" value={label} onInput={(ev) => setLabel((ev.target as HTMLInputElement).value)} />
          </label>
          <div class="field">
            <span class="field-label">Spaces</span>
            {named.length === 0 ? (
              <p class="muted small">No spaces to scope to yet; create one on the Spaces page.</p>
            ) : (
              <div class="check-list">
                {named.map((s) => (
                  <label key={s.name} class="check">
                    <input
                      type="checkbox"
                      checked={scope.includes(s.name)}
                      onChange={(ev) => {
                        const on = (ev.target as HTMLInputElement).checked
                        setScope((sc) => (on ? [...sc, s.name] : sc.filter((x) => x !== s.name)))
                      }}
                    />
                    {s.label || s.name}
                  </label>
                ))}
              </div>
            )}
          </div>
          <label class="check">
            <input type="checkbox" checked={canWrite} onChange={(ev) => setCanWrite((ev.target as HTMLInputElement).checked)} />
            Can write: create, append to, and move notes
          </label>
          <p class="form-msg" role="status">
            {msg}
          </p>
          <div class="form-actions">
            <button type="submit" class="btn primary" disabled={busy || label.trim() === '' || scope.length === 0}>
              <Icon name="key" />
              Create key
            </button>
          </div>
        </form>
      </Block>
    </>
  )
}

// --- backups -----------------------------------------------------------------

const SCHEDULES: Array<{ value: RemoteSchedule; label: string; hint?: string }> = [
  { value: 'commit', label: 'After every commit' },
  { value: 'hourly', label: 'Hourly' },
  { value: 'nightly', label: 'Nightly' },
]

function scheduleLabel(r: GitRemote): string {
  switch (r.schedule) {
    case 'commit':
      return 'after every commit'
    case 'hourly':
      return 'hourly'
    default:
      return `nightly at ${String(r.push_hour).padStart(2, '0')}:00`
  }
}

interface RemoteDraft {
  name: string
  url: string
  schedule: RemoteSchedule
  pushHour: number
  enabled: boolean
}

const emptyDraft: RemoteDraft = { name: '', url: '', schedule: 'nightly', pushHour: 2, enabled: true }

function draftOf(r: GitRemote): RemoteDraft {
  return { name: r.name, url: r.url, schedule: r.schedule, pushHour: r.push_hour, enabled: r.enabled }
}

/** An absolute path is a bare repository on a mounted disk; anything else
 * is SSH and has a key. The server draws the same line. */
export const isPathURL = (url: string): boolean => url.trim().startsWith('/')

/** A remote's public key with a Copy button; Task 8's setup flow shares it. */
export function KeyBlock({ publicKey }: { publicKey: string }) {
  const [note, setNote] = useState('')
  return (
    <div class="key-block">
      <code class="key-value">{publicKey}</code>
      <div class="key-actions">
        <button type="button" class="btn small" onClick={() => void copyText(publicKey, setNote, 'the key')}>
          <Icon name="copy" />
          Copy key
        </button>
        <span class="muted" role="status">
          {note}
        </span>
      </div>
    </div>
  )
}

/** Backup remotes: where the history is pushed, and when. Owner-only. */
function BackupsBlock({ user, say, confirm, onStatus }: { user: auth.User | null; say: Ctx['say']; confirm: Ctx['confirm']; onStatus: () => void }) {
  const owner = !user || user.is_owner
  const [remotes, setRemotes] = useState<GitRemote[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [editing, setEditing] = useState<string | 'new' | null>(null)
  const [draft, setDraft] = useState<RemoteDraft>(emptyDraft)
  const [msg, setMsg] = useState('')
  // The key made when "Add remote" opens, held until the remote is saved.
  const [newKey, setNewKey] = useState<{ public_key: string; sealed_key: string } | null>(null)
  const [keyOpen, setKeyOpen] = useState<string | null>(null)

  const reload = useCallback(async () => {
    try {
      const r = await api.gitRemotes()
      setRemotes(r.remotes)
      setError(null)
    } catch (err) {
      setError(msgOf(err, 'Could not load the remotes.'))
    }
  }, [])
  useEffect(() => {
    if (owner) void reload()
  }, [owner, reload])

  if (!owner) {
    return (
      <Block title="Backups" lead="The history can be pushed to other git repositories as an off-site copy.">
        <p class="muted">The owner account manages backup remotes.</p>
      </Block>
    )
  }

  const startNew = () => {
    setDraft(emptyDraft)
    setMsg('')
    setNewKey(null)
    setKeyOpen(null)
    setEditing('new')
    api
      .newGitKey()
      .then(setNewKey)
      .catch((err: unknown) => setMsg(msgOf(err, 'Could not make a key.')))
  }
  const startEdit = (r: GitRemote) => {
    setDraft(draftOf(r))
    setMsg('')
    setKeyOpen(null)
    setEditing(r.id)
  }
  const cancel = () => {
    setEditing(null)
    setMsg('')
  }

  const save = (ev: Event) => {
    ev.preventDefault()
    if (editing === null) return
    const input: GitRemoteInput = {
      name: draft.name.trim(),
      url: draft.url.trim(),
      schedule: draft.schedule,
      push_hour: draft.pushHour,
      enabled: draft.enabled,
    }
    setBusy('save')
    setMsg('')
    if (editing === 'new') {
      if (!isPathURL(draft.url)) input.sealed_key = newKey?.sealed_key
      api
        .createGitRemote(input)
        .then(({ remote, push_error }) => {
          void reload()
          onStatus()
          if (push_error) {
            // Saved, but the first push failed: stay on the remote so the
            // owner can fix the key and push again, not add it twice.
            setDraft(draftOf(remote))
            setEditing(remote.id)
            setMsg(push_error)
            return
          }
          say(`Added ${remote.name}.`)
          setEditing(null)
        })
        .catch((err: unknown) => setMsg(msgOf(err, 'Could not add the remote.')))
        .finally(() => setBusy(null))
      return
    }
    api
      .updateGitRemote(editing, input)
      .then((r) => {
        say(`Saved ${r.name}.`)
        setEditing(null)
        void reload()
        onStatus()
      })
      .catch((err: unknown) => setMsg(msgOf(err, 'Could not save the remote.')))
      .finally(() => setBusy(null))
  }

  const makeKey = (r: GitRemote) => {
    const run = () => {
      setBusy(r.id)
      api
        .replaceGitKey(r.id)
        .then(() => say(`Made a new key for ${r.name}. Add it to the repository.`))
        .catch((err: unknown) => say(msgOf(err, 'Could not make a key.')))
        .finally(() => {
          setBusy(null)
          void reload()
        })
    }
    if (r.public_key === '') return run()
    confirm({
      title: `Make a new key for ${r.name}?`,
      body: 'The old key stops working. Add the new one to the repository.',
      confirmLabel: 'Make a new key',
      danger: true,
      onConfirm: run,
    })
  }

  const forgetHost = (r: GitRemote) => {
    setBusy(r.id)
    api
      .forgetHostKey(r.id)
      .then(() => say(`Forgot the host key for ${r.name}. The next push learns it again.`))
      .catch((err: unknown) => say(msgOf(err, 'Could not forget the host key.')))
      .finally(() => {
        setBusy(null)
        void reload()
      })
  }

  const push = (r: GitRemote) => {
    setBusy(r.id)
    api
      .pushGitRemote(r.id)
      .then(() => say(`Pushed to ${r.name}.`))
      .catch((err: unknown) => say(msgOf(err, `Could not push to ${r.name}.`)))
      .finally(() => {
        setBusy(null)
        void reload()
        onStatus()
      })
  }

  const test = (r: GitRemote) => {
    setBusy(r.id)
    api
      .testGitRemote(r.id)
      .then((res) => say(res.branches === 0 ? `${r.name} is reachable and empty.` : `${r.name} is reachable with ${res.branches} ${res.branches === 1 ? 'branch' : 'branches'}.`))
      .catch((err: unknown) => say(msgOf(err, `Could not reach ${r.name}.`)))
      .finally(() => setBusy(null))
  }

  const remove = (r: GitRemote) => {
    confirm({
      title: `Remove ${r.name}?`,
      body: 'The server stops pushing to it. Nothing already pushed is touched, and the local history stays as it is.',
      confirmLabel: 'Remove',
      danger: true,
      onConfirm: () => {
        setBusy(r.id)
        api
          .deleteGitRemote(r.id)
          .then(() => {
            say(`Removed ${r.name}.`)
            if (editing === r.id) setEditing(null)
            if (keyOpen === r.id) setKeyOpen(null)
            void reload()
            onStatus()
          })
          .catch((err: unknown) => say(msgOf(err, 'Could not remove the remote.')))
          .finally(() => setBusy(null))
      },
    })
  }

  const current = editing !== 'new' ? remotes?.find((r) => r.id === editing) : undefined
  // The key block hides while the address is a folder path.
  const showKey = !isPathURL(draft.url) && (editing === 'new' || current?.public_key !== '')
  const keyText = editing === 'new' ? newKey?.public_key : current?.public_key
  const form = (
    <form class="settings-form" onSubmit={save}>
      <label class="field">
        <span class="field-label">Name</span>
        <input class="input" type="text" required maxLength={48} placeholder="github" value={draft.name} onInput={(ev) => setDraft({ ...draft, name: (ev.target as HTMLInputElement).value })} />
      </label>
      {showKey && (
        <div class="field">
          <span class="field-label">1 · Add this key to the repository, with write access</span>
          {keyText ? <KeyBlock publicKey={keyText} /> : <span class="muted">{editing === 'new' && !msg ? 'Making a key…' : 'No key yet.'}</span>}
        </div>
      )}
      <label class="field">
        <span class="field-label">{showKey ? '2 · ' : ''}Repository address</span>
        <input class="input mono" type="text" required placeholder="git@github.com:you/notes.git" value={draft.url} onInput={(ev) => setDraft({ ...draft, url: (ev.target as HTMLInputElement).value })} />
        <span class="field-hint">SSH (git@host:path or ssh://…) or a bare repository on a mounted disk.</span>
      </label>
      <Choice label="Push" value={draft.schedule} options={SCHEDULES} onChange={(v) => setDraft({ ...draft, schedule: v })} />
      {draft.schedule === 'nightly' && (
        <label class="field">
          <span class="field-label">At (hour, server time)</span>
          <input class="input narrow" type="number" min={0} max={23} value={draft.pushHour} onInput={(ev) => setDraft({ ...draft, pushHour: Number((ev.target as HTMLInputElement).value) })} />
        </label>
      )}
      {editing !== 'new' && (
        <label class="check">
          <input type="checkbox" checked={draft.enabled} onChange={(ev) => setDraft({ ...draft, enabled: (ev.target as HTMLInputElement).checked })} />
          Enabled
        </label>
      )}
      <p class="form-msg" role="status">
        {msg}
      </p>
      <div class="form-actions">
        <button type="submit" class="btn primary" disabled={busy !== null || draft.name.trim() === '' || draft.url.trim() === '' || (editing === 'new' && !isPathURL(draft.url) && !newKey)}>
          <Icon name="save" />
          {editing === 'new' ? 'Test and add' : 'Save'}
        </button>
        <button type="button" class="btn" onClick={cancel}>
          Cancel
        </button>
      </div>
    </form>
  )

  return (
    <Block
      title="Backups"
      lead="Push the history to other git repositories over SSH, or to a bare repository on another disk. Each SSH remote has its own key. Each push carries only the commits that remote has not seen."
    >
      {error ? (
        <p class="error">{error}</p>
      ) : !remotes ? (
        <p class="muted">Loading…</p>
      ) : remotes.length === 0 && editing !== 'new' ? (
        <p class="muted">No remotes yet. The history lives only on this server's disk.</p>
      ) : (
        <ul class="settings-list">
          {remotes.map((r) => (
            <li key={r.id} class="settings-row remote-row">
              <Icon name="commit" class="settings-row-icon" />
              <div class="settings-row-main">
                <span class="settings-row-title">
                  {r.name}
                  <span class="badge">{scheduleLabel(r)}</span>
                  {!r.enabled && <span class="badge warn">off</span>}
                </span>
                <span class="settings-row-sub">
                  <code>{r.url}</code> · {r.pushes > 0 ? `pushed ${r.pushes} ${r.pushes === 1 ? 'time' : 'times'}, last ${fmtDate(r.last_push)}` : 'never pushed'}
                </span>
                {r.last_error && (
                  <span class="settings-row-sub error">
                    {fmtDate(r.last_error_at)}: {r.last_error}
                  </span>
                )}
              </div>
              <div class="settings-row-actions">
                {!isPathURL(r.url) && (
                  <button type="button" class="btn small" aria-expanded={keyOpen === r.id} disabled={busy !== null} onClick={() => setKeyOpen(keyOpen === r.id ? null : r.id)}>
                    <Icon name="key" />
                    Key
                  </button>
                )}
                <button type="button" class="btn small" disabled={busy !== null} onClick={() => push(r)}>
                  <Icon name="commit" />
                  Push now
                </button>
                <button type="button" class="btn small" disabled={busy !== null} onClick={() => test(r)}>
                  <Icon name="link" />
                  Test
                </button>
                <button type="button" class="btn small" disabled={busy !== null} onClick={() => startEdit(r)}>
                  <Icon name="pencil" />
                  Edit
                </button>
                <button type="button" class="btn small danger" disabled={busy !== null} onClick={() => remove(r)}>
                  <Icon name="x" />
                  Remove
                </button>
              </div>
              {keyOpen === r.id && (
                <div class="remote-form key-panel">
                  <p class="field-hint">Add this key to {r.url} with write access. It only opens that repository.</p>
                  {r.public_key ? <KeyBlock publicKey={r.public_key} /> : <p class="muted">No key yet.</p>}
                  <div class="key-actions">
                    <button type="button" class="btn small" disabled={busy !== null} onClick={() => makeKey(r)}>
                      <Icon name="refresh" />
                      {r.public_key ? 'Make a new key' : 'Make a key'}
                    </button>
                  </div>
                  {r.host_key && (
                    <p class="field-hint">
                      Host <code>{r.host_key}</code>{' '}
                      <button type="button" class="btn small" disabled={busy !== null} onClick={() => forgetHost(r)}>
                        Forget
                      </button>
                    </p>
                  )}
                </div>
              )}
              {editing === r.id && <div class="remote-form">{form}</div>}
            </li>
          ))}
        </ul>
      )}
      {editing === 'new' ? (
        <div class="remote-form">{form}</div>
      ) : (
        <div class="form-actions start">
          <button type="button" class="btn" disabled={busy !== null} onClick={startNew}>
            <Icon name="plus" />
            Add a remote
          </button>
        </div>
      )}
    </Block>
  )
}

// --- restore from a backup ---------------------------------------------------

const RELATIONS: Record<string, string> = {
  identical: 'the same history as this server',
  ahead: 'ahead of this server’s history',
  behind: 'behind this server’s history',
  diverged: 'diverged from this server’s history',
}

/** Restore from a backup, under Data › History. The server fetches the
 * backup, shows what it holds, and moves the tree to it once the owner
 * types the remote's name. Owner-only. */
function RestoreBlock({ user, say, onChanged, onStatus }: { user: auth.User | null; say: Ctx['say']; onChanged: () => void; onStatus: () => void }) {
  const owner = !user || user.is_owner
  const [remotes, setRemotes] = useState<GitRemote[] | null>(null)
  const [pick, setPick] = useState('')
  const [preview, setPreview] = useState<RestorePreview | null>(null)
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')

  const enabled = useMemo(() => (remotes ?? []).filter((r) => r.enabled), [remotes])
  const reload = useCallback(async () => {
    try {
      const r = await api.gitRemotes()
      setRemotes(r.remotes)
    } catch {
      setRemotes([])
    }
  }, [])
  useEffect(() => {
    if (owner) void reload()
  }, [owner, reload])
  useEffect(() => {
    const first = enabled[0]
    if (first && !enabled.some((r) => r.id === pick)) setPick(first.id)
  }, [enabled, pick])

  if (!owner) return null
  if (remotes === null) return null
  const remote = enabled.find((r) => r.id === pick) ?? enabled[0]
  if (!remote) return null

  const fetchPreview = () => {
    setBusy(true)
    setMsg('')
    api
      .restorePreview(remote.id)
      .then((r) => {
        setPreview(r.preview)
        setTyped('')
      })
      .catch((err: unknown) => setMsg(msgOf(err, `Could not read ${remote.name}.`)))
      .finally(() => setBusy(false))
  }

  const restore = (ev: Event) => {
    ev.preventDefault()
    if (typed.trim() !== remote.name) return
    setBusy(true)
    setMsg('')
    api
      .restoreGitRemote(remote.id, typed.trim())
      .then((r) => {
        const parts: string[] = []
        if (r.added > 0) parts.push(`${r.added} added`)
        if (r.changed > 0) parts.push(`${r.changed} changed`)
        if (r.deleted > 0) parts.push(`${r.deleted} deleted`)
        say(parts.length > 0 ? `Restored from ${remote.name}: ${parts.join(', ')}.` : `Restored from ${remote.name}; the tree already matched.`)
        setPreview(null)
        setTyped('')
        onChanged()
        onStatus()
      })
      .catch((err: unknown) => setMsg(msgOf(err, 'Could not restore.')))
      .finally(() => setBusy(false))
  }

  return (
    <Block
      title="Restore from a backup"
      lead="Bring the tree back from a backup remote: the disaster-recovery path for the notes."
    >
      <div class="pref-row">
        <label class="pref-label" for="restore-remote">
          Backup
          <span class="pref-hint">An enabled remote the server pushes to. Nothing is touched until the restore is confirmed.</span>
        </label>
        <div class="pref-controls">
          <select id="restore-remote" class="select grow" value={remote.id} disabled={busy} onChange={(ev) => {
            setPick((ev.target as HTMLSelectElement).value)
            setPreview(null)
            setTyped('')
            setMsg('')
          }}>
            {enabled.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
          </select>
          <button type="button" class="btn" disabled={busy} onClick={fetchPreview}>
            <Icon name="refresh" />
            {preview && preview.commit ? 'Refresh' : 'Preview'}
          </button>
        </div>
      </div>
      {preview && (
        <form class="settings-form" onSubmit={restore}>
          <dl class="meta facts">
            <dt>Holds</dt>
            <dd>
              {preview.commits} {preview.commits === 1 ? 'commit' : 'commits'}, {preview.notes} {preview.notes === 1 ? 'note' : 'notes'}
            </dd>
            <dt>Newest</dt>
            <dd>
              {preview.newest.subject} — {preview.newest.name}, {fmtDate(preview.newest.date)}
            </dd>
            <dt>Stands as</dt>
            <dd>{RELATIONS[preview.relation] ?? preview.relation}</dd>
          </dl>
          <p class="muted">
            Restoring moves every note and file to what the backup holds. The current state is committed and tagged first, so it stays reachable in the
            history. Accounts, sessions, agent tokens, public links and the trash belong to this server and are not replaced; nobody is signed out.
          </p>
          <label class="field">
            <span class="field-label">
              Type <strong>{remote.name}</strong> to confirm
            </span>
            <input
              class="input"
              type="text"
              autocomplete="off"
              spellcheck={false}
              value={typed}
              onInput={(ev) => setTyped((ev.target as HTMLInputElement).value)}
            />
          </label>
          <p class="form-msg" role="status">
            {msg}
          </p>
          <div class="form-actions">
            <button type="submit" class="btn danger" disabled={busy || typed.trim() !== remote.name}>
              <Icon name="restore" />
              Restore from {remote.name}
            </button>
            <button type="button" class="btn" disabled={busy} onClick={() => { setPreview(null); setTyped(''); setMsg('') }}>
              Cancel
            </button>
          </div>
        </form>
      )}
    </Block>
  )
}

// --- appearance --------------------------------------------------------------

/** Re-renders on any preference change, so pickers follow each other. */
function usePrefs(): void {
  const [, bump] = useState(0)
  useEffect(() => prefs.onChange(() => bump((n) => n + 1)), [])
}

function ThemePicker({ mode }: { mode: Mode }) {
  usePrefs()
  const current = prefs.themeFor(mode).id
  const list = prefs.themes().filter((d) => d.mode === mode)
  const label = mode === 'light' ? 'Light theme' : 'Dark theme'
  return (
    <div class="theme-pick">
      <span class="pref-label">{label}</span>
      <div class="theme-grid" role="radiogroup" aria-label={label}>
        {list.map((d) => {
          const [bg, ink, accent] = swatch(d)
          return (
            <button
              key={d.id}
              type="button"
              role="radio"
              aria-checked={d.id === current}
              class={'theme-swatch' + (d.id === current ? ' on' : '')}
              onClick={() => {
                // Picking a theme shows it, unless the system decides the mode.
                if (prefs.theme() !== 'system') prefs.setTheme(mode)
                prefs.setThemeFor(mode, d.id)
              }}
            >
              <span class="theme-swatch-preview" style={{ background: bg }}>
                <i style={{ background: ink }} />
                <i style={{ background: accent }} />
              </span>
              <span class="theme-swatch-name">{d.name}</span>
            </button>
          )
        })}
      </div>
    </div>
  )
}

function OwnThemes() {
  usePrefs()
  const [text, setText] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)
  const own = prefs.customThemes()
  const owner = auth.user()?.is_owner ?? true

  function save(list: ThemeDef[], pick?: ThemeDef): void {
    setBusy(true)
    api
      .saveThemes(list.map(({ name, mode, colors }) => ({ name, mode, colors })))
      .then((saved) => {
        prefs.setCustomThemes(saved)
        if (pick) {
          if (prefs.theme() !== 'system') prefs.setTheme(pick.mode)
          prefs.setThemeFor(pick.mode, pick.id)
          setText('')
        }
        setMsg('')
      })
      .catch((err: unknown) => setMsg(err instanceof Error ? err.message : 'Could not save the themes.'))
      .finally(() => setBusy(false))
  }

  function add(): void {
    const def = parseTheme(text)
    if (typeof def === 'string') {
      setMsg(def)
      return
    }
    save([...own.filter((d) => d.id !== def.id), def], def)
  }

  function startFromCurrent(): void {
    const mode = document.documentElement.dataset['theme'] === 'dark' ? 'dark' : 'light'
    setText(exportTheme(prefs.themeFor(mode), document.documentElement))
    setMsg('')
  }

  return (
    <div class="theme-own">
      <span class="pref-label">Your themes</span>
      <p class="settings-lead">
        Kept with the notes in <code>.yana/themes.json</code>, so every device and account sees them.
        {owner
          ? ' Paste a theme as JSON: a name, a mode (light or dark) and hex colours. Only bg, ink and accent are needed; the rest are mixed from them.'
          : ' The owner account adds them.'}
      </p>
      {own.length > 0 && (
        <ul class="theme-list">
          {own.map((d) => (
            <li key={d.id}>
              <span>
                {d.name} <span class="pref-hint">{d.mode}</span>
              </span>
              {owner && (
                <button type="button" class="btn" disabled={busy} onClick={() => save(own.filter((o) => o.id !== d.id))}>
                  Remove
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {owner && (
        <>
          <textarea
            class="input theme-paste"
            spellcheck={false}
            rows={8}
            placeholder={'{\n  "name": "My theme",\n  "mode": "dark",\n  "colors": { "bg": "#1e1e2e", "ink": "#cdd6f4", "accent": "#cba6f7" }\n}'}
            value={text}
            onInput={(ev) => setText((ev.target as HTMLTextAreaElement).value)}
          />
          <p class="form-msg" role="status">
            {msg}
          </p>
          <div class="form-actions">
            <button type="button" class="btn primary" disabled={busy || !text.trim()} onClick={add}>
              Add theme
            </button>
            <button type="button" class="btn" onClick={startFromCurrent}>
              Start from the current theme
            </button>
          </div>
        </>
      )}
    </div>
  )
}

function AppearanceSection({ ctx: _ctx }: { ctx: Ctx }) {
  const [theme, setTheme] = useState(prefs.theme)
  const [size, setSize] = useState(prefs.textSize)
  const [width, setWidth] = useState(prefs.lineWidth)
  const [centered, setCentered] = useState(prefs.centered)
  const [open, setOpen] = useState(prefs.openMode)
  const [live, setLive] = useState(prefs.livePreview)
  const [density, setDensity] = useState(prefs.density)
  const [reveal, setReveal] = useState(prefs.revealInTree)
  const [suggest, setSuggest] = useState(prefs.suggestNames)
  // Picking a theme can switch the mode too.
  useEffect(() => prefs.onChange(() => setTheme(prefs.theme())), [])

  return (
    <>
      <Block title="Theme">
        <Choice
          label="Colours"
          value={theme}
          options={[
            { value: 'system', label: 'Match the system' },
            { value: 'light', label: 'Light' },
            { value: 'dark', label: 'Dark' },
          ]}
          onChange={(v) => {
            prefs.setTheme(v)
            setTheme(v)
          }}
        />
        <ThemePicker mode="light" />
        <ThemePicker mode="dark" />
        <OwnThemes />
      </Block>
      <Block title="Text" lead="The note body, reading and editing alike.">
        <Choice
          label="Size"
          value={size}
          options={[
            { value: 'small', label: 'Small' },
            { value: 'normal', label: 'Normal' },
            { value: 'large', label: 'Large' },
          ]}
          onChange={(v) => {
            prefs.setTextSize(v)
            setSize(v)
          }}
        />
        <Choice
          label="Line width"
          value={width}
          options={[
            { value: 'narrow', label: 'Narrow' },
            { value: 'normal', label: 'Normal' },
            { value: 'wide', label: 'Wide' },
          ]}
          onChange={(v) => {
            prefs.setLineWidth(v)
            setWidth(v)
          }}
        />
        <Toggle
          label="Center the text"
          hint="The column sits in the middle of the page instead of against its left edge."
          on={centered}
          onChange={(v) => {
            prefs.setCentered(v)
            setCentered(v)
          }}
        />
      </Block>
      <Block title="Editing">
        <Choice
          label="Open notes"
          value={open}
          options={[
            { value: 'read', label: 'To read' },
            { value: 'edit', label: 'To edit' },
            { value: 'split', label: 'Side by side', hint: 'On a phone this reads' },
          ]}
          onChange={(v) => {
            prefs.setOpenMode(v)
            setOpen(v)
          }}
        />
        <Toggle
          label="Hide markdown syntax while editing"
          hint="Marks show only on the line the caret is on."
          on={live}
          onChange={(v) => {
            prefs.setLivePreview(v)
            setLive(v)
          }}
        />
        <Toggle
          label="Suggest names for new notes"
          hint="In a folder of dated or numbered notes, the next name shows in grey. Right takes it."
          on={suggest}
          onChange={(v) => {
            prefs.setSuggestNames(v)
            setSuggest(v)
          }}
        />
      </Block>
      <Block title="Sidebar">
        <Choice
          label="Density"
          value={density}
          options={[
            { value: 'comfortable', label: 'Comfortable' },
            { value: 'compact', label: 'Compact' },
          ]}
          onChange={(v) => {
            prefs.setDensity(v)
            setDensity(v)
          }}
        />
        <Toggle
          label="Reveal the open note"
          hint="A note opened from a link, search or a tab opens the folders above it in the tree and scrolls to it."
          on={reveal}
          onChange={(v) => {
            prefs.setRevealInTree(v)
            setReveal(v)
          }}
        />
      </Block>
      <p class="muted small">Kept in this browser. Each device has its own.</p>
    </>
  )
}

// --- data --------------------------------------------------------------------

/** Every live public link in the caller's spaces, each with its own
 * Revoke, and one button for all of them. */
function PublicLinksBlock({ ctx }: { ctx: Ctx }) {
  const { say, confirm, onOpen, onChanged } = ctx
  const [links, setLinks] = useState<PublicLinkRow[] | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  const load = useCallback(() => {
    api
      .publicLinks()
      .then((r) => setLinks(r.links))
      .catch((err: unknown) => {
        setLinks([])
        say(msgOf(err, 'Could not list the public links.'))
      })
  }, [say])
  useEffect(load, [load])

  const revoke = (l: PublicLinkRow) => {
    setBusy(l.id)
    api
      .revokePublicLink(l.note_id)
      .then(() => {
        say(`Revoked the link to ${l.title || l.path}.`)
        load()
        onChanged()
      })
      .catch((err: unknown) => say(msgOf(err, 'Could not revoke the link.')))
      .finally(() => setBusy(null))
  }

  const revokeAll = () => {
    confirm({
      title: 'Revoke every public link?',
      body: 'Each link stops working at once. Sharing a note again makes a new address.',
      confirmLabel: 'Revoke all',
      danger: true,
      onConfirm: () => {
        setBusy('all')
        api
          .revokeAllPublicLinks()
          .then((r) => {
            say(r.revoked === 1 ? 'Revoked one link.' : `Revoked ${r.revoked} links.`)
            load()
            onChanged()
          })
          .catch((err: unknown) => say(msgOf(err, 'Could not revoke the links.')))
          .finally(() => setBusy(null))
      },
    })
  }

  return (
    <Block title="Public links" lead="Notes anyone with the link can read, with no account. A link stops working the moment it is revoked.">
      {links === null ? (
        <p class="muted">Loading…</p>
      ) : links.length === 0 ? (
        <p class="muted">No note has a public link. Share one from the note's menu.</p>
      ) : (
        <>
          <ul class="settings-list">
            {links.map((l) => (
              <li key={l.id} class="settings-row">
                <Icon name="globe" class="settings-row-icon" />
                <div class="settings-row-main">
                  <a
                    class="settings-row-title"
                    href={`/n/${l.note_id}`}
                    onClick={(ev) => {
                      ev.preventDefault()
                      onOpen(l.note_id)
                    }}
                  >
                    {l.title || l.path}
                  </a>
                  <span class="settings-row-sub">
                    {l.path} · shared {fmtDate(l.created_at)} · {l.expires_at ? `stops ${fmtDate(l.expires_at)}` : 'no expiry'}
                  </span>
                </div>
                <div class="settings-row-actions">
                  <button type="button" class="btn small" disabled={busy !== null} onClick={() => void copyText(l.url, say, 'the link')}>
                    <Icon name="copy" />
                    Copy
                  </button>
                  <button type="button" class="btn small danger" disabled={busy !== null} onClick={() => revoke(l)}>
                    <Icon name="unlink" />
                    Revoke
                  </button>
                </div>
              </li>
            ))}
          </ul>
          <div class="form-actions start">
            <button type="button" class="btn danger" disabled={busy !== null} onClick={revokeAll}>
              <Icon name="unlink" />
              Revoke all
            </button>
          </div>
        </>
      )}
    </Block>
  )
}

/** Assets no note references, each with a size and a Trash button — the
 * only way this page removes anything: to .trash, never a delete. */
function OrphanAssetsBlock({ ctx }: { ctx: Ctx }) {
  const { say, confirm } = ctx
  const [assets, setAssets] = useState<OrphanAsset[] | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  const load = useCallback(() => {
    api
      .assetOrphans()
      .then((r) => setAssets(r.assets))
      .catch((err: unknown) => {
        setAssets([])
        say(msgOf(err, 'Could not list unreferenced files.'))
      })
  }, [say])
  useEffect(load, [load])

  const trash = (a: OrphanAsset) => {
    confirm({
      title: `Move ${a.path.slice(a.path.lastIndexOf('/') + 1)} to the trash?`,
      body: 'The file moves to .trash, recoverable for the same window as a deleted note. Nothing is deleted outright.',
      confirmLabel: 'Move to trash',
      onConfirm: () => {
        setBusy(a.path)
        api
          .trashAsset(a.path)
          .then(() => {
            say('Moved to the trash.')
            load()
          })
          .catch((err: unknown) => say(msgOf(err, 'Could not trash the file.')))
          .finally(() => setBusy(null))
      },
    })
  }

  return (
    <Block title="Unreferenced files" lead="Files under _assets/ no note links to. Listed so nothing is orphaned by accident; moving one to the trash never deletes it outright.">
      {assets === null ? (
        <p class="muted">Loading…</p>
      ) : assets.length === 0 ? (
        <p class="muted">Nothing unreferenced.</p>
      ) : (
        <ul class="settings-list">
          {assets.map((a) => (
            <li key={a.path} class="settings-row">
              <Icon name="file-text" class="settings-row-icon" />
              <div class="settings-row-main">
                <span class="settings-row-title">{a.path.slice(a.path.lastIndexOf('/') + 1)}</span>
                <span class="settings-row-sub mono">{a.path} · {fmtBytes(a.size)}</span>
              </div>
              <button type="button" class="btn small" disabled={busy !== null} onClick={() => trash(a)}>
                <Icon name="trash" />
                Trash
              </button>
            </li>
          ))}
        </ul>
      )}
    </Block>
  )
}

/** Deleted notes, under Data: every note whose file is gone, with what
 * a restore would bring it back from — the trash copy, the retained
 * edits, or the history. The one-note-gone-by-accident path: no commit
 * log to read. */
function DeletedNotesBlock({ ctx }: { ctx: Ctx }) {
  const { say, confirm, onChanged, onOpen } = ctx
  const [entries, setEntries] = useState<DeletedNote[] | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  const load = useCallback(() => {
    api
      .deletedNotes()
      .then((r) => setEntries(r.entries))
      .catch((err: unknown) => {
        setEntries([])
        say(msgOf(err, 'Could not list the deleted notes.'))
      })
  }, [say])
  useEffect(load, [load])

  const source = (e: DeletedNote): string => {
    if (e.has_file) return 'trash copy'
    if (e.has_sidecar) return 'recent edits'
    return 'the history'
  }

  const restore = (e: DeletedNote) => {
    if (busy || !e.id) return
    confirm({
      title: `Restore ${e.title || e.path}?`,
      body: e.has_file
        ? 'The note returns to its original path, or to a free name beside whatever now lives there.'
        : 'The note returns as it was last committed, to its original path or a free name beside whatever now lives there.',
      confirmLabel: 'Restore',
      onConfirm: () => {
        setBusy(e.id)
        api
          .restoreDeleted(e.id)
          .then((r) => {
            setBusy(null)
            load()
            onChanged()
            if (r.conflict) say(`A note now lives at ${e.path}; restored beside it as ${r.path}.`)
            else say(`Restored ${r.path} from ${r.from === 'history' ? 'the history' : 'the trash'}.`)
            if (r.note?.id && !r.deferred) onOpen(r.note.id)
          })
          .catch((err: unknown) => {
            setBusy(null)
            say(msgOf(err, 'Could not restore the note.'))
          })
      },
    })
  }

  if (entries === null) {
    return (
      <Block title="Deleted notes" lead="Notes whose files are gone, each restorable to where it lived.">
        <p class="muted">Loading…</p>
      </Block>
    )
  }
  if (entries.length === 0) {
    return (
      <Block title="Deleted notes" lead="Notes whose files are gone, each restorable to where it lived.">
        <p class="muted">Nothing deleted to bring back.</p>
      </Block>
    )
  }

  return (
    <Block title="Deleted notes" lead="Notes whose files are gone, each restorable to where it lived — from the trash while it holds them, and from the history after that.">
      <ul class="settings-list">
        {entries.map((e) => (
          <li key={e.id + e.deleted_at} class="settings-row">
            <Icon name="file-text" class="settings-row-icon" />
            <div class="settings-row-main">
              <span class="settings-row-title">{e.title || e.path}</span>
              <span class="settings-row-sub mono">
                {e.path} · deleted {fmtDate(e.deleted_at)} · from {source(e)}
              </span>
            </div>
            <div class="settings-row-actions">
              <button type="button" class="btn small" disabled={busy !== null || !e.id} onClick={() => restore(e)}>
                <Icon name="restore" />
                Restore
              </button>
            </div>
          </li>
        ))}
      </ul>
    </Block>
  )
}

/** Conflict copies: every one in the caller's spaces, with its age and
 * size, and the resolve dialog a click away. A copy whose original is
 * gone opens as the plain note it is. */
function ConflictsBlock({ ctx }: { ctx: Ctx }) {
  const { say, onChanged } = ctx
  const [rows, setRows] = useState<ConflictEntry[] | null>(null)
  const [resolving, setResolving] = useState<string | null>(null)

  const load = useCallback(() => {
    api
      .conflicts()
      .then((r) => setRows(r.conflicts))
      .catch((err: unknown) => {
        setRows([])
        say(msgOf(err, 'Could not list the conflicts.'))
      })
  }, [say])
  useEffect(load, [load])

  return (
    <Block title="Conflicts" lead="Copies parked beside a note when two writes met the same path: a restore over a newer note, or a save over a file that changed underneath it.">
      {rows === null ? (
        <p class="muted">Loading…</p>
      ) : rows.length === 0 ? (
        <p class="muted">No conflicts waiting.</p>
      ) : (
        <ul class="settings-list">
          {rows.map((c) => (
            <li key={c.note.id} class="settings-row">
              <Icon name="alert" class="settings-row-icon conflict-row-icon" />
              <div class="settings-row-main">
                <span class="settings-row-title">{c.note.title || c.note.path}</span>
                <span class="settings-row-sub mono">
                  {c.note.path} · {fmtAge(c.note.mtime)} · {fmtBytes(c.note.size)}
                  {c.of ? ` · beside ${c.of.title || c.of.path}` : ' · its original is gone'}
                </span>
              </div>
              {c.of ? (
                <button type="button" class="btn small" onClick={() => setResolving(c.of?.id ?? null)}>
                  <Icon name="check" />
                  Resolve
                </button>
              ) : (
                <button type="button" class="btn small" onClick={() => ctx.onOpen(c.note.id)}>
                  <Icon name="book-open" />
                  Open
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {resolving && (
        <ConflictDialog
          noteId={resolving}
          onClose={() => setResolving(null)}
          onToast={say}
          onResolved={() => {
            load()
            onChanged()
          }}
        />
      )}
    </Block>
  )
}

function DataSection({ ctx }: { ctx: Ctx }) {
  const { user, status, spaces, notes, say, confirm, onChanged, onOpenTrash, onStatus } = ctx
  const recent = useMemo(() => {
    const byId = new Map(notes.map((n) => [n.id, n]))
    return prefs.recents().map((id) => byId.get(id)).find((n) => n !== undefined)
  }, [notes])
  const [noteID, setNoteID] = useState(recent?.id ?? notes[0]?.id ?? '')
  const [space, setSpace] = useState(prefs.defaultSpace)
  const [busy, setBusy] = useState<string | null>(null)

  useEffect(() => {
    if (!noteID && notes.length > 0) setNoteID(recent?.id ?? notes[0]?.id ?? '')
  }, [notes, noteID, recent])

  const run = (id: string, p: Promise<{ blob: Blob; name: string }>, done: string) => {
    setBusy(id)
    p.then(({ blob, name }) => {
      saveBlob(blob, name)
      say(done)
    })
      .catch((err: unknown) => say(msgOf(err, 'Could not export.')))
      .finally(() => setBusy(null))
  }

  const snapshot = () => {
    setBusy('snapshot')
    api
      .gitSnapshot()
      .then((r) => {
        say(r.commits > 0 ? `Committed ${r.commits === 1 ? 'a snapshot' : `${r.commits} snapshots`}.` : 'Nothing to commit; the history is up to date.')
        onStatus()
      })
      .catch((err: unknown) => say(msgOf(err, 'Could not commit now.')))
      .finally(() => setBusy(null))
  }

  const spaceName = space === '' ? 'the root' : space
  const git = status?.git
  const sorted = useMemo(() => [...notes].sort((a, b) => a.path.localeCompare(b.path)), [notes])

  return (
    <>
      <Block title="Export" lead="Everything leaves in a form that stands on its own.">
        <div class="pref-row">
          <label class="pref-label" for="export-note">
            One note as HTML
            <span class="pref-hint">A single file with images and styles inside it.</span>
          </label>
          <div class="pref-controls">
            <select id="export-note" class="select grow" value={noteID} disabled={notes.length === 0} onChange={(ev) => setNoteID((ev.target as HTMLSelectElement).value)}>
              {sorted.map((n) => (
                <option key={n.id} value={n.id}>
                  {n.title || n.path}
                </option>
              ))}
            </select>
            <button type="button" class="btn" disabled={busy !== null || !noteID} onClick={() => run('note', api.exportNote(noteID), 'Exported as a single HTML file.')}>
              <Icon name="download" />
              Export
            </button>
          </div>
        </div>
        <div class="pref-row">
          <label class="pref-label" for="export-space">
            A space
            <span class="pref-hint">As a site: offline HTML with navigation and search. As a zip: the markdown and assets, unchanged.</span>
          </label>
          <div class="pref-controls">
            <SpaceSelect id="export-space" value={space} spaces={(spaces ?? []).filter((s) => s.name !== '')} any="the root" onChange={setSpace} />
            <button type="button" class="btn" disabled={busy !== null} onClick={() => run('site', api.exportSite(space), `Exported ${spaceName} as a static site.`)}>
              <Icon name="download" />
              Site
            </button>
            <button type="button" class="btn" disabled={busy !== null} onClick={() => run('zip', api.exportTree(space), `Exported ${spaceName} as a zip.`)}>
              <Icon name="download" />
              Zip
            </button>
          </div>
        </div>
      </Block>
      <PublicLinksBlock ctx={ctx} />
      <ConflictsBlock ctx={ctx} />
      <OrphanAssetsBlock ctx={ctx} />
      <Block title="Trash" lead={`Deleted notes stay recoverable for ${status?.trash?.retention_days ?? 30} days, then go for good. Emptying the trash is the only permanent deletion.`}>
        <div class="form-actions start">
          <button type="button" class="btn" onClick={onOpenTrash}>
            <Icon name="trash" />
            Open the trash
          </button>
        </div>
      </Block>
      <DeletedNotesBlock ctx={ctx} />
      <Block title="History" lead="The notes root is a git repository. The server commits after the tree has been quiet; a snapshot commits now.">
        {git ? (
          git.available ? (
            <dl class="meta facts">
              <dt>Commits</dt>
              <dd>{git.commits}</dd>
              <dt>Last commit</dt>
              <dd>{git.commits > 0 ? fmtDate(git.last_commit) : 'none yet'}</dd>
              {git.remotes > 0 && (
                <>
                  <dt>Last push</dt>
                  <dd>{isSet(git.last_push) ? fmtDate(git.last_push) : 'none yet'}</dd>
                </>
              )}
              {git.errors > 0 && (
                <>
                  <dt>Errors</dt>
                  <dd class="error">
                    {git.errors} since the server started
                    {git.last_error && (
                      <>
                        ; the latest, {fmtDate(git.last_error_at)}: <code class="error-detail">{git.last_error}</code>
                      </>
                    )}
                  </dd>
                </>
              )}
            </dl>
          ) : (
            <p class="muted">Git is not available on the server, so there is no history to commit to.</p>
          )
        ) : (
          <p class="muted">History is off on this server.</p>
        )}
        {git?.available && (
          <div class="form-actions start">
            <button type="button" class="btn" disabled={busy !== null} onClick={snapshot}>
              <Icon name="commit" />
              Snapshot now
            </button>
          </div>
        )}
      </Block>
      {git?.available && <RestoreBlock user={user} say={say} onChanged={onChanged} onStatus={onStatus} />}
      {git?.available && <BackupsBlock user={user} say={say} confirm={confirm} onStatus={onStatus} />}
      <Block title="Index" lead="What the server knows about the tree. The files are the truth; the index can be deleted and rebuilt at any time.">
        {status ? (
          <dl class="meta facts">
            <dt>State</dt>
            <dd>{status.ready ? 'ready' : 'indexing'}</dd>
            <dt>Last scan</dt>
            <dd>{status.last_scan ? fmtDate(status.last_scan) : 'not yet'}</dd>
            <dt>Notes</dt>
            <dd>{status.notes}</dd>
            <dt>Assets</dt>
            <dd>{status.assets}</dd>
            {status.sync && (
              <>
                <dt>Open documents</dt>
                <dd>
                  {status.sync.loaded}
                  {status.sync.dirty > 0 ? `, ${status.sync.dirty} waiting to write` : ''}
                </dd>
              </>
            )}
            <dt>Regex search</dt>
            <dd>{status.regex_search ? `ripgrep ${status.regex_version ?? ''}`.trim() : 'off: ripgrep is not installed on the server'}</dd>
            <dt>Accounts</dt>
            <dd>{status.accounts ? 'on' : 'off'}</dd>
            <dt>Server</dt>
            <dd class="mono">{status.version}</dd>
          </dl>
        ) : (
          <p class="muted">Loading…</p>
        )}
      </Block>
    </>
  )
}
