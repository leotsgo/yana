-- yana in neovim: browse and edit the notes on the server `yana login`
-- signed in to. `yana` at a shell launches neovim with this plugin and
-- calls launch(); in your own neovim, :Yana opens it. See :Yana help.
local api = require('yana.api')
local buf = require('yana.buf')
local pick = require('yana.pick')

local M = {}

M.config = {
  keys = { find = '<leader>sf', grep = '<leader>sg', recent = '<leader><leader>' },
  -- Global keys are set when yana launched neovim; true sets them in any
  -- neovim, false never.
  global_keys = nil,
  picker = 'auto', -- 'telescope', 'select' or 'auto'
  live = true, -- refresh unmodified notes changed elsewhere
  space = nil, -- the space yana opens in, instead of the list of spaces
}

function M.setup(opts)
  M.config = vim.tbl_deep_extend('force', M.config, opts or {})
  if M.config.global_keys or (M.config.global_keys == nil and M.launched) then
    M.set_keys()
  end
end

local function notify(msg, level)
  vim.notify('yana: ' .. msg, level or vim.log.levels.INFO)
end

--- Ask before applying a folder listing's changes. Tests replace it.
function M.confirm(lines)
  local msg = 'Apply these changes?\n\n' .. table.concat(lines, '\n')
  return vim.fn.confirm(msg, '&Yes\n&No', 2) == 1
end

-- --- the tree ---------------------------------------------------------------------

-- What the plugin knows of the server: its tree, cached.
M.state = { server = nil, host = nil, entries = nil, notes = {}, by_path = {}, by_id = {} }

local waiting, fetching = {}, false

function M.refresh_tree(cb)
  if cb then
    waiting[#waiting + 1] = cb
  end
  if fetching then
    return
  end
  fetching = true
  api.call({ 'tree' }, nil, function(err, t)
    fetching = false
    local cbs = waiting
    waiting = {}
    if err then
      notify(err.msg, vim.log.levels.ERROR)
      return
    end
    local s = M.state
    s.server, s.host, s.entries = t.server, t.host, t.entries
    s.notes, s.by_path, s.by_id = {}, {}, {}
    for _, e in ipairs(t.entries) do
      s.by_path[e.path] = e
      if not e.dir then
        s.notes[#s.notes + 1] = e
        s.by_id[e.id] = e
      end
    end
    for b in pairs(buf.folders) do
      if vim.api.nvim_buf_is_valid(b) then
        buf.render_folder(b)
      end
    end
    M.watch_all()
    for _, f in ipairs(cbs) do
      f(s.notes)
    end
  end)
end

--- cb(notes) once the tree is known.
function M.with_tree(cb)
  if M.state.entries then
    return cb(M.state.notes)
  end
  M.refresh_tree(cb)
end

--- One folder's entries: folders first, then notes, by name.
function M.listing(dir)
  local out = {}
  for _, e in ipairs(M.state.entries or {}) do
    local parent = e.path:match('^(.*)/[^/]*$') or ''
    if parent == dir then
      out[#out + 1] = e
    end
  end
  table.sort(out, function(a, b)
    if (a.dir or false) ~= (b.dir or false) then
      return a.dir == true
    end
    return a.name:lower() < b.name:lower()
  end)
  return out
end

-- --- names ------------------------------------------------------------------------

function M.host()
  return M.state.host or M.launch_host or ''
end

--- The buffer name for a note path, or a folder's (dir true; '' is the top).
function M.name(path, dir)
  local name = 'yana://' .. M.host() .. '/' .. path
  if dir and path ~= '' then
    name = name .. '/'
  end
  return name
end

function M.parse(name)
  local host, path = name:match('^yana://([^/]*)/(.*)$')
  if not host then
    return nil
  end
  local dir = path == '' or path:sub(-1) == '/'
  return { host = host, path = (path:gsub('/+$', '')), dir = dir }
end

--- Where yana starts: the default space when one is set, else the spaces.
function M.home()
  return M.name(M.config.space or '', true)
end

--- Edit a yana:// name; focus is a path for the cursor in a listing.
function M.edit(name, focus)
  local b = vim.fn.bufnr(name)
  if focus and b > 0 and buf.folders[b] then
    buf.folders[b].focus = focus
  end
  vim.cmd.edit(vim.fn.fnameescape(name))
  b = vim.api.nvim_get_current_buf()
  if focus and buf.folders[b] then
    buf.folders[b].focus = focus
    if M.state.entries then
      buf.render_folder(b, false)
      buf.folders[b].focus = nil
    end
  end
end

--- Open the note at path, at the line at(text) names when given.
function M.open_note(path, at)
  M.with_tree(function()
    local e = M.state.by_path[path]
    if e and e.kind ~= 'md' and e.kind ~= 'html' then
      return vim.ui.open(M.state.server .. '/n/' .. e.id)
    end
    local name = M.name(path)
    local b = vim.fn.bufnr(name)
    if b > 0 and vim.api.nvim_buf_is_loaded(b) then
      vim.cmd.buffer(b)
      if at and buf.notes[b] then
        local line = at(buf.buffer_text(b))
        if line then
          pcall(vim.api.nvim_win_set_cursor, 0, { line, 0 })
        end
      end
      return
    end
    M.pending_at = at
    vim.cmd.edit(vim.fn.fnameescape(name))
  end)
end

--- Open the daily note for day (YYYY-MM-DD), made when missing.
function M.open_day(day)
  api.call({ 'daily', day }, nil, function(err, e)
    if err then
      return notify(err.msg, vim.log.levels.ERROR)
    end
    M.refresh_tree(function()
      M.open_note(e.path)
    end)
  end)
end

local function relday(days)
  local t = os.date('*t')
  return os.date('%Y-%m-%d', os.time({ year = t.year, month = t.month, day = t.day + days, hour = 12 }))
end

-- The folder the current buffer is in, for :New and :Mkdir.
local function current_dir()
  local p = M.parse(vim.api.nvim_buf_get_name(0))
  if not p then
    return nil
  end
  if p.dir then
    return p.path
  end
  return p.path:match('^(.*)/[^/]*$') or ''
end

local function join(dir, name)
  if name:find('/', 1, true) and not name:match('^[^/]*/$') then
    return name
  end
  return dir == '' and name or (dir .. '/' .. name)
end

function M.new_note(name)
  local dir = current_dir()
  if not dir then
    return notify(':New works from a yana buffer, or give it a path like space/name', vim.log.levels.WARN)
  end
  api.call({ 'create', join(dir, name) }, nil, function(err, e)
    if err then
      return notify(err.msg, vim.log.levels.ERROR)
    end
    M.refresh_tree(function()
      M.open_note(e.path)
    end)
  end)
end

function M.mkdir(name)
  local dir = current_dir() or ''
  api.call({ 'create', join(dir, (name:gsub('/*$', '/'))) }, nil, function(err, e)
    if err then
      return notify(err.msg, vim.log.levels.ERROR)
    end
    M.refresh_tree(function()
      notify('made ' .. e.path .. '/')
    end)
  end)
end

-- --- live updates -------------------------------------------------------------------

local watcher, watched = nil, ''
local timers = {}

local function debounce(key, ms, f)
  if timers[key] then
    timers[key]:stop()
  end
  timers[key] = vim.defer_fn(function()
    timers[key] = nil
    f()
  end, ms)
end

local function on_change(ev)
  if ev.watching or ev.error then
    M.watching = M.watching or {}
    if ev.watching then
      M.watching[ev.watching] = true
    end
    return
  end
  for b, n in pairs(buf.notes) do
    if n.id == ev.id and vim.api.nvim_buf_is_valid(b) then
      debounce('note' .. b, 150, function()
        buf.refresh_note(b)
      end)
    end
  end
  -- A move, a new note or a delete changes listings.
  if not M.state.by_id[ev.id] or (ev.path ~= '' and M.state.by_id[ev.id].path ~= ev.path) then
    debounce('tree', 300, function()
      M.refresh_tree()
    end)
  end
end

--- Watch every space this account sees (the relay takes up to 32 per
--- connection), restarting the watcher when that set changes.
-- ponytail: one watch per session over every space, not per open note.
function M.watch_all()
  if not M.config.live then
    return
  end
  local spaces = {}
  for _, e in ipairs(M.state.entries or {}) do
    if e.dir and not e.path:find('/', 1, true) and #spaces < 32 then
      spaces[#spaces + 1] = e.path
    end
  end
  local key = table.concat(spaces, '\n')
  if key == watched and watcher then
    return
  end
  if watcher then
    pcall(watcher.kill, watcher, 15)
    watcher = nil
  end
  watched = key
  if #spaces > 0 then
    watcher = api.watch(spaces, on_change)
  end
end

function M.watch_space()
  if not watcher then
    M.watch_all()
  end
end

-- --- remembered buffers ---------------------------------------------------------------

local function state_file()
  local base = vim.env.XDG_STATE_HOME
  if not base or base:sub(1, 1) ~= '/' then
    base = vim.env.HOME .. '/.local/state'
  end
  return base .. '/yana/nvim.json'
end

local function read_state()
  local f = io.open(state_file(), 'r')
  if not f then
    return {}
  end
  local ok, v = pcall(vim.json.decode, f:read('*a'))
  f:close()
  return ok and type(v) == 'table' and v or {}
end

local function write_state(all)
  vim.fn.mkdir(vim.fn.fnamemodify(state_file(), ':h'), 'p', tonumber('700', 8))
  local tmp = state_file() .. '.tmp'
  local f = io.open(tmp, 'w')
  if f then
    f:write(vim.json.encode(all))
    f:close()
    os.rename(tmp, state_file())
  end
end

-- Cursor positions, by buffer name: shada keeps marks for yana:// names
-- but never applies them back, so the plugin keeps its own.
M.positions = nil

function M.last_position(b)
  local name = vim.api.nvim_buf_get_name(b)
  local at = M.pending_at
  M.pending_at = nil
  if at then
    return at(buf.buffer_text(b))
  end
  local p = (M.positions or {})[name]
  if p then
    return { math.min(p[1], vim.api.nvim_buf_line_count(b)), p[2] }
  end
end

local function remember_position(b)
  local name = vim.api.nvim_buf_get_name(b)
  if not vim.startswith(name, 'yana://') or not buf.notes[b] or not vim.b[b].yana_loaded then
    return
  end
  local w = vim.fn.bufwinid(b)
  if w > 0 then
    M.positions = M.positions or {}
    M.positions[name] = vim.api.nvim_win_get_cursor(w)
  end
end

--- The yana buffers to reopen next time: listed ones, in order, and
--- which one was current.
function M.save_state()
  if not M.state.server then
    return
  end
  local names, current = {}, nil
  for _, b in ipairs(vim.api.nvim_list_bufs()) do
    local name = vim.api.nvim_buf_get_name(b)
    if vim.bo[b].buflisted and vim.startswith(name, 'yana://' .. M.host() .. '/') then
      names[#names + 1] = name
      remember_position(b)
    end
  end
  local name = vim.api.nvim_buf_get_name(0)
  if vim.startswith(name, 'yana://') then
    current = name
  end
  local all = read_state()
  local pos = vim.tbl_extend('force', (all[M.state.server] or {}).positions or {}, M.positions or {})
  all[M.state.server] = { buffers = names, current = current, positions = pos }
  write_state(all)
end

-- Saves still running when neovim is asked to quit: wait for them, so a
-- failed one marks its buffer modified and the quit stops there.
local function wait_saves()
  vim.wait(15000, function()
    for _, n in pairs(buf.notes) do
      if n.saving then
        return false
      end
    end
    return true
  end, 20)
end

-- --- keys and commands ---------------------------------------------------------------

function M.set_keys()
  local k = M.config.keys
  local set = function(lhs, f, desc)
    if lhs then
      vim.keymap.set('n', lhs, f, { desc = 'yana: ' .. desc })
    end
  end
  set(k.find, function()
    pick.find()
  end, 'find notes')
  set(k.grep, function()
    pick.grep()
  end, 'search note text')
  set(k.recent, function()
    pick.recent()
  end, 'recent notes')
end

M.subcommands = {
  today = function()
    M.open_day(relday(0))
  end,
  yesterday = function()
    M.open_day(relday(-1))
  end,
  tomorrow = function()
    M.open_day(relday(1))
  end,
  new = function(arg)
    if arg == '' then
      return notify('usage: :Yana new <name>', vim.log.levels.WARN)
    end
    M.new_note(arg)
  end,
  mkdir = function(arg)
    if arg == '' then
      return notify('usage: :Yana mkdir <name>', vim.log.levels.WARN)
    end
    M.mkdir(arg)
  end,
  find = function(arg)
    pick.find(arg)
  end,
  grep = function(arg)
    pick.grep(arg)
  end,
  recent = function(arg)
    pick.recent(arg)
  end,
  open = function(arg)
    if arg == '' then
      return M.edit(M.name('', true))
    end
    M.open_note((arg:gsub('^/', '')))
  end,
  whoami = function()
    api.call({ 'whoami' }, nil, function(err, w)
      if err then
        return notify(err.msg, vim.log.levels.ERROR)
      end
      notify(w.username .. ' on ' .. w.server)
    end)
  end,
  logout = function()
    api.call({ 'logout' }, nil, function(err)
      if err then
        return notify(err.msg, vim.log.levels.ERROR)
      end
      notify('signed out')
      if M.launched then
        M.state.server = nil -- nothing to remember for a session that ended
        vim.cmd('confirm qall')
      end
    end)
  end,
  help = function()
    buf.show_help(buf.folders[vim.api.nvim_get_current_buf()] and 'folder' or 'note')
  end,
}

function M.command(opts)
  local sub, arg = (opts.args or ''):match('^%s*(%S*)%s*(.-)%s*$')
  if sub == '' then
    -- :Yana alone: home, after the tree is known (for the host).
    return M.with_tree(function()
      M.edit(M.home())
    end)
  end
  local f = M.subcommands[sub]
  if not f then
    return notify('no subcommand ' .. sub, vim.log.levels.ERROR)
  end
  M.with_tree(function()
    f(arg)
  end)
end

function M.complete(lead, line)
  local words = vim.split(line, '%s+')
  if #words <= 2 then
    return vim.tbl_filter(function(s)
      return vim.startswith(s, lead)
    end, vim.tbl_keys(M.subcommands))
  end
  if words[2] == 'open' then
    local out = {}
    for _, e in ipairs(M.state.entries or {}) do
      if not e.dir and vim.startswith(e.path, lead) then
        out[#out + 1] = e.path
      end
    end
    return out
  end
  return {}
end

-- Short commands in a session yana launched.
local aliases = {
  Today = 'today',
  Yesterday = 'yesterday',
  Tomorrow = 'tomorrow',
  New = 'new',
  Mkdir = 'mkdir',
  Sf = 'find',
  Sg = 'grep',
  Recent = 'recent',
  Whoami = 'whoami',
  Logout = 'logout',
}

--- Called by `yana` after the person's own config has loaded.
---@param opts { server: string, host: string, open?: string, picker?: string, query?: string, init?: string }
function M.launch(opts)
  M.launched = true
  M.launch_host = opts.host
  M.state.server = M.state.server or opts.server
  for name, sub in pairs(aliases) do
    vim.api.nvim_create_user_command(name, function(o)
      M.command({ args = sub .. ' ' .. (o.args or '') })
    end, { nargs = '*', desc = 'yana ' .. sub })
  end
  if opts.init then
    local ok, err = pcall(dofile, opts.init)
    if not ok then
      notify(opts.init .. ': ' .. tostring(err), vim.log.levels.ERROR)
    end
  end
  if M.config.global_keys ~= false then
    M.set_keys()
  end
  local group = vim.api.nvim_create_augroup('yana_launch', { clear = true })
  vim.api.nvim_create_autocmd('QuitPre', { group = group, callback = wait_saves })
  vim.api.nvim_create_autocmd('VimLeavePre', { group = group, callback = M.save_state })

  local first = vim.api.nvim_get_current_buf()
  local saved = read_state()[opts.server] or {}
  M.positions = saved.positions or {}
  local prefix = 'yana://' .. opts.host .. '/'
  for _, name in ipairs(saved.buffers or {}) do
    if vim.startswith(name, prefix) then
      vim.cmd.badd(vim.fn.fnameescape(name))
    end
  end
  local target = saved.current
  if opts.open then
    target = M.name(opts.open)
  elseif not target or not vim.startswith(target, prefix) or target == prefix then
    target = M.home()
  end
  M.edit(target)
  -- The empty buffer neovim started with is not left behind in the list.
  if
    first ~= vim.api.nvim_get_current_buf()
    and vim.api.nvim_buf_get_name(first) == ''
    and not vim.bo[first].modified
    and vim.api.nvim_buf_line_count(first) == 1
    and vim.api.nvim_buf_get_lines(first, 0, 1, false)[1] == ''
  then
    pcall(vim.api.nvim_buf_delete, first, {})
  end
  if opts.picker and opts.picker ~= '' then
    local run = function()
      vim.schedule(function()
        pick[({ sf = 'find', sg = 'grep', recent = 'recent' })[opts.picker]](opts.query)
      end)
    end
    if vim.v.vim_did_enter == 1 then
      run()
    else
      vim.api.nvim_create_autocmd('VimEnter', { once = true, callback = run })
    end
  end
end

-- Positions are kept as buffers are left, too, not only at exit.
vim.api.nvim_create_autocmd('BufLeave', {
  pattern = 'yana://*',
  callback = function(ev)
    remember_position(ev.buf)
  end,
})

return M
