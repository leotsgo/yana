-- yana:// buffers. A folder is an editable listing like oil.nvim: each
-- line is "/<n> name" with the /<n> concealed, naming the entry it
-- started as, so :w can tell a rename from a delete and a create. A note
-- is its text; :w saves through the server's merge.
local api = require('yana.api')

local M = {}

local ns = vim.api.nvim_create_namespace('yana')

-- Per note buffer: { id, kind, path, base, hash, role, eol, saving, again }
M.notes = {}
-- Per folder buffer: { dir, entries = { [n] = entry } }
M.folders = {}

local function yana()
  return require('yana')
end

local function notify(msg, level)
  vim.notify('yana: ' .. msg, level or vim.log.levels.INFO)
end

-- --- text helpers -----------------------------------------------------------

local function split_text(text)
  local lines = vim.split(text, '\n', { plain = true })
  local eol = lines[#lines] == ''
  if eol and #lines > 1 then
    lines[#lines] = nil
  end
  return lines, eol
end

function M.buffer_text(buf)
  local lines = vim.api.nvim_buf_get_lines(buf, 0, -1, false)
  local text = table.concat(lines, '\n')
  local n = M.notes[buf]
  if (n == nil or n.eol) and text ~= '' then
    text = text .. '\n'
  end
  return text
end

-- vim.diff hunks as { s, e, lines }: base lines [s, e) (0-based) become lines.
local function hunks(a, b)
  local bl = vim.split(b, '\n', { plain = true })
  local out = {}
  for _, h in ipairs(vim.diff(a, b, { result_type = 'indices', algorithm = 'histogram' })) do
    local sa, ca, sb, cb = h[1], h[2], h[3], h[4]
    local s = ca == 0 and sa or sa - 1
    out[#out + 1] = { s = s, e = s + ca, lines = cb == 0 and {} or vim.list_slice(bl, sb, sb + cb - 1) }
  end
  return out
end

-- vim.diff wants whole lines; texts are compared with a final newline.
local function nl(s)
  return s:sub(-1) == '\n' and s or s .. '\n'
end

--- Put text in buf by changing only the lines that differ, so the cursor,
--- marks and folds elsewhere stay where they were.
function M.set_text(buf, text, opts)
  opts = opts or {}
  local lines, eol = split_text(text)
  local cur = table.concat(vim.api.nvim_buf_get_lines(buf, 0, -1, false), '\n') .. '\n'
  local new = table.concat(lines, '\n') .. '\n'
  local mod = vim.bo[buf].modifiable
  vim.bo[buf].modifiable = true
  local ul
  if opts.no_undo then
    ul = vim.bo[buf].undolevels
    vim.bo[buf].undolevels = -1
  end
  local hs = hunks(cur, new)
  for i = #hs, 1, -1 do
    vim.api.nvim_buf_set_lines(buf, hs[i].s, hs[i].e, false, hs[i].lines)
  end
  if opts.no_undo then
    vim.bo[buf].undolevels = ul
  end
  vim.bo[buf].modifiable = mod
  if M.notes[buf] then
    M.notes[buf].eol = eol
  end
end

--- Three-way line merge: the edits base→ours applied onto theirs (itself
--- base plus other edits). nil when the two touch the same lines.
function M.merge3(base, ours, theirs)
  base, ours, theirs = nl(base), nl(ours), nl(theirs)
  local all = {}
  for _, h in ipairs(hunks(base, ours)) do
    all[#all + 1] = h
  end
  for _, h in ipairs(hunks(base, theirs)) do
    all[#all + 1] = h
  end
  table.sort(all, function(a, b)
    return a.s < b.s or (a.s == b.s and a.e < b.e)
  end)
  local bl = vim.split(base, '\n', { plain = true })
  bl[#bl] = nil
  local out, at = {}, 0
  local prev
  for _, h in ipairs(all) do
    if prev and h.s == prev.s and h.e == prev.e and vim.deep_equal(h.lines, prev.lines) then
      goto continue -- the same edit on both sides
    end
    if prev and (h.s < prev.e or h.s == prev.s) then
      return nil
    end
    for i = at + 1, h.s do
      out[#out + 1] = bl[i]
    end
    vim.list_extend(out, h.lines)
    at, prev = h.e, h
    ::continue::
  end
  for i = at + 1, #bl do
    out[#out + 1] = bl[i]
  end
  return table.concat(out, '\n') .. '\n'
end

-- --- help -----------------------------------------------------------------------

M.help = {}

local function map(buf, kind, lhs, rhs, desc)
  vim.keymap.set('n', lhs, rhs, { buffer = buf, desc = 'yana: ' .. desc, nowait = true })
  M.help[kind] = M.help[kind] or {}
  for _, h in ipairs(M.help[kind]) do
    if h[1] == lhs then
      return
    end
  end
  table.insert(M.help[kind], { lhs, desc })
end

function M.show_help(kind)
  local lines = { '# yana ' .. kind, '' }
  for _, h in ipairs(M.help[kind] or {}) do
    lines[#lines + 1] = string.format('  %-10s %s', h[1], h[2])
  end
  vim.list_extend(lines, {
    '',
    'Commands: :Yana {today|yesterday|tomorrow|new|mkdir|find|grep|recent|open|whoami|logout}',
  })
  if kind == 'folder' then
    vim.list_extend(lines, {
      'Edit the listing like text, then :w — rename a line to rename or move,',
      'add a line to make a note (end it in / for a folder), delete a line to',
      'send it to the trash. Every change is confirmed first.',
    })
  end
  vim.lsp.util.open_floating_preview(lines, 'markdown', { border = 'rounded', focus_id = 'yana_help' })
end

-- --- folder buffers -------------------------------------------------------------

local function display(e)
  return e.dir and (e.name .. '/') or e.name
end

--- The entry on line row (1-based) of a folder buffer, if the line still
--- carries its id.
function M.entry_at(buf, row)
  local f = M.folders[buf]
  local line = vim.api.nvim_buf_get_lines(buf, row - 1, row, false)[1] or ''
  local n = line:match('^/(%d+) ')
  return f and n and f.entries[tonumber(n)]
end

local function conceal_ids(buf)
  vim.api.nvim_buf_clear_namespace(buf, ns, 0, -1)
  for i, line in ipairs(vim.api.nvim_buf_get_lines(buf, 0, -1, false)) do
    local prefix = line:match('^/%d+ ')
    if prefix then
      vim.api.nvim_buf_set_extmark(buf, ns, i - 1, 0, { end_col = #prefix, conceal = '' })
    end
  end
end

--- Draw a folder's listing from the cached tree. A listing with edits not
--- yet written is left alone unless force.
function M.render_folder(buf, force)
  local f = M.folders[buf]
  if not f or (vim.bo[buf].modified and not force) then
    return
  end
  local cur = vim.api.nvim_buf_get_lines(buf, 0, -1, false)
  local entries = yana().listing(f.dir)
  f.entries = entries
  local lines = {}
  for i, e in ipairs(entries) do
    lines[i] = '/' .. i .. ' ' .. display(e)
  end
  if vim.deep_equal(cur, lines) then
    vim.bo[buf].modified = false
    return
  end
  local wins = vim.fn.win_findbuf(buf)
  local views = {}
  for _, w in ipairs(wins) do
    views[w] = vim.api.nvim_win_call(w, vim.fn.winsaveview)
  end
  vim.bo[buf].modifiable = true
  local ul = vim.bo[buf].undolevels
  vim.bo[buf].undolevels = -1
  vim.api.nvim_buf_set_lines(buf, 0, -1, false, lines)
  vim.bo[buf].undolevels = ul
  vim.bo[buf].modified = false
  conceal_ids(buf)
  for w, v in pairs(views) do
    vim.api.nvim_win_call(w, function()
      vim.fn.winrestview(v)
    end)
  end
  if f.focus then
    for i, e in ipairs(entries) do
      if e.path == f.focus then
        for _, w in ipairs(wins) do
          vim.api.nvim_win_set_cursor(w, { i, 0 })
        end
      end
    end
    f.focus = nil
  end
end

local function open_entry(e)
  if not e then
    return
  end
  if e.dir then
    return yana().edit(yana().name(e.path, true))
  end
  yana().open_note(e.path)
end

local function folder_parent(dir)
  return dir:match('^(.*)/[^/]*$') or ''
end

local preview = { win = nil, buf = nil }

local function update_preview(buf)
  if not (preview.win and vim.api.nvim_win_is_valid(preview.win)) then
    return
  end
  local e = M.entry_at(buf, vim.api.nvim_win_get_cursor(0)[1])
  local function show(lines, ft)
    if vim.api.nvim_buf_is_valid(preview.buf) then
      vim.bo[preview.buf].modifiable = true
      vim.api.nvim_buf_set_lines(preview.buf, 0, -1, false, lines)
      vim.bo[preview.buf].modifiable = false
      vim.bo[preview.buf].filetype = ft or ''
    end
  end
  if not e then
    return show({})
  end
  if e.dir then
    return show(vim.tbl_map(display, yana().listing(e.path)))
  end
  if e.kind ~= 'md' and e.kind ~= 'html' then
    return show({ '(a ' .. tostring(e.kind) .. '; <CR> opens it in the app)' })
  end
  api.call({ 'read', e.id, e.kind }, nil, function(err, n)
    if M.entry_at(buf, vim.api.nvim_win_get_cursor(0)[1]) == e then
      show(err and { err.msg } or vim.split(n.text, '\n', { plain = true }), e.kind == 'html' and 'html' or 'markdown')
    end
  end)
end

local function toggle_preview(buf)
  if preview.win and vim.api.nvim_win_is_valid(preview.win) then
    vim.api.nvim_win_close(preview.win, true)
    preview.win = nil
    return
  end
  local from = vim.api.nvim_get_current_win()
  preview.buf = vim.api.nvim_create_buf(false, true)
  vim.bo[preview.buf].bufhidden = 'wipe'
  preview.win = vim.api.nvim_open_win(preview.buf, false, { split = 'right', win = from })
  update_preview(buf)
end

-- The changes a folder listing's text asks for.
function M.folder_changes(buf)
  local f = M.folders[buf]
  local join = function(name)
    return f.dir == '' and name or (f.dir .. '/' .. name)
  end
  local actions, seen = {}, {}
  for _, line in ipairs(vim.api.nvim_buf_get_lines(buf, 0, -1, false)) do
    local n, rest = line:match('^/(%d+) (.*)$')
    local name = vim.trim(rest or line)
    local e = n and f.entries[tonumber(n)]
    if name ~= '' then
      if e and not seen[e] then
        seen[e] = true
        if name ~= display(e) and name ~= e.name then
          actions[#actions + 1] = { op = 'move', e = e, to = join(name:gsub('/+$', '')) }
        end
      else
        local dir = name:sub(-1) == '/'
        actions[#actions + 1] = { op = dir and 'mkdir' or 'create', path = join(name) }
      end
    end
  end
  for _, e in ipairs(f.entries) do
    if not seen[e] then
      actions[#actions + 1] = { op = 'delete', e = e }
    end
  end
  return actions
end

local function describe(a)
  if a.op == 'move' then
    return 'RENAME  ' .. display(a.e) .. '  →  ' .. a.to
  elseif a.op == 'delete' then
    return 'DELETE  ' .. a.e.path .. (a.e.dir and '/' or '') .. '  (to the trash)'
  elseif a.op == 'mkdir' then
    return 'MKDIR   ' .. a.path
  end
  return 'CREATE  ' .. a.path
end

local function apply(actions, i, done)
  local a = actions[i]
  if not a then
    return done()
  end
  local args
  if a.op == 'move' then
    args = { 'move', a.e.dir and (a.e.path .. '/') or a.e.id, a.to }
  elseif a.op == 'delete' then
    args = { 'delete', a.e.dir and (a.e.path .. '/') or a.e.id }
  else
    args = { 'create', a.path }
  end
  api.call(args, nil, function(err)
    if err then
      notify(describe(a) .. ': ' .. err.msg, vim.log.levels.ERROR)
      return done(err)
    end
    apply(actions, i + 1, done)
  end)
end

function M.write_folder(buf)
  local actions = M.folder_changes(buf)
  if #actions == 0 then
    vim.bo[buf].modified = false
    return M.render_folder(buf, true)
  end
  local lines = vim.tbl_map(describe, actions)
  if not yana().confirm(lines) then
    return notify('nothing changed; the listing keeps your edits')
  end
  apply(actions, 1, function(err)
    yana().refresh_tree(function()
      M.render_folder(buf, true)
      if not err then
        notify(#actions == 1 and lines[1] or (#actions .. ' changes applied'))
      end
    end)
  end)
end

function M.read_folder(buf, dir)
  M.folders[buf] = M.folders[buf] or { dir = dir, entries = {} }
  M.folders[buf].dir = dir
  vim.bo[buf].buftype = 'acwrite'
  vim.bo[buf].swapfile = false
  vim.bo[buf].filetype = 'yana'
  local function setwin()
    vim.wo[0][0].conceallevel = 3
    vim.wo[0][0].concealcursor = 'nvic'
    vim.wo[0][0].wrap = false
  end
  if vim.api.nvim_get_current_buf() == buf then
    setwin()
  end
  if not M.folders[buf].hooked then
    M.folders[buf].hooked = true
    local group = vim.api.nvim_create_augroup('yana_folder_' .. buf, { clear = true })
    vim.api.nvim_create_autocmd('BufWinEnter', { group = group, buffer = buf, callback = setwin })
    vim.api.nvim_create_autocmd({ 'TextChanged', 'TextChangedI' }, {
      group = group,
      buffer = buf,
      callback = function()
        conceal_ids(buf)
      end,
    })
    vim.api.nvim_create_autocmd('CursorMoved', {
      group = group,
      buffer = buf,
      callback = function()
        -- Keep the cursor off the hidden id.
        local row, col = unpack(vim.api.nvim_win_get_cursor(0))
        local prefix = (vim.api.nvim_buf_get_lines(buf, row - 1, row, false)[1] or ''):match('^/%d+ ')
        if prefix and col < #prefix then
          vim.api.nvim_win_set_cursor(0, { row, #prefix })
        end
        update_preview(buf)
      end,
    })
    local cr = function()
      local e = M.entry_at(buf, vim.api.nvim_win_get_cursor(0)[1])
      if not e then
        return notify('a new line: :w makes it first')
      end
      open_entry(e)
    end
    map(buf, 'folder', '<CR>', cr, 'open the folder or note')
    map(buf, 'folder', '-', function()
      local d = M.folders[buf].dir
      if d == '' then
        return
      end
      local parent = folder_parent(d)
      yana().edit(yana().name(parent, true), d)
    end, 'up a folder')
    map(buf, 'folder', '_', function()
      yana().edit(yana().home())
    end, 'the default space, else the spaces')
    map(buf, 'folder', '<C-p>', function()
      toggle_preview(buf)
    end, 'toggle the preview')
    map(buf, 'folder', 'g?', function()
      M.show_help('folder')
    end, 'this help')
    map(buf, 'folder', 'g.', '<Nop>', 'nothing (no hidden files here)')
  end
  vim.bo[buf].modified = false
  yana().with_tree(function()
    M.render_folder(buf, true)
  end)
end

-- --- note buffers -----------------------------------------------------------------

--- Fill a note buffer from the server. at(text) may name the line to
--- start on.
function M.read_note(buf, path, at)
  vim.bo[buf].buftype = 'acwrite'
  vim.bo[buf].swapfile = false
  local ext = path:match('%.(%w+)$')
  vim.bo[buf].filetype = (ext == 'html' or ext == 'htm') and 'html' or 'markdown'
  vim.bo[buf].modifiable = false
  M.note_maps(buf)
  yana().with_tree(function()
    local e = yana().state.by_path[path]
    if not e then
      M.notes[buf] = { path = path, eol = true }
      vim.bo[buf].modifiable = true
      vim.bo[buf].modified = false
      return notify(path .. ' is a new note: :w makes it')
    end
    if e.kind ~= 'md' and e.kind ~= 'html' then
      local url = yana().state.server .. '/n/' .. e.id
      M.notes[buf] = { path = path, id = e.id, kind = e.kind, role = 'viewer' }
      vim.bo[buf].modifiable = true
      vim.api.nvim_buf_set_lines(buf, 0, -1, false, { 'A ' .. e.kind .. ' opens in the app: ' .. url })
      vim.bo[buf].modifiable = false
      vim.bo[buf].modified = false
      vim.ui.open(url)
      return
    end
    api.call({ 'read', e.id, e.kind }, nil, function(err, n)
      if not vim.api.nvim_buf_is_valid(buf) then
        return
      end
      if err then
        return notify('could not read ' .. path .. ': ' .. err.msg, vim.log.levels.ERROR)
      end
      M.notes[buf] = { path = path, id = n.id, kind = n.kind, base = n.base, hash = n.hash, role = n.role, eol = true }
      M.set_text(buf, n.text, { no_undo = true })
      vim.bo[buf].modified = false
      local viewer = n.role == 'viewer'
      vim.bo[buf].modifiable = not viewer
      vim.bo[buf].readonly = viewer
      if viewer then
        notify(path .. ' is read-only for you (viewer)', vim.log.levels.WARN)
      end
      api.call({ 'recent', '--push', n.id }, nil, function() end)
      local line = at and at(n.text) or yana().last_position(buf)
      if line then
        for _, w in ipairs(vim.fn.win_findbuf(buf)) do
          pcall(vim.api.nvim_win_set_cursor, w, type(line) == 'table' and line or { line, 0 })
        end
      end
      vim.b[buf].yana_loaded = true
      yana().watch_space(path)
    end)
  end)
end

--- Reread an unmodified note changed elsewhere; edits in progress are
--- never overwritten (the next :w merges them).
function M.refresh_note(buf)
  local n = M.notes[buf]
  if not n or not n.id or n.saving or vim.bo[buf].modified or (n.kind ~= 'md' and n.kind ~= 'html') then
    return
  end
  local tick = vim.b[buf].changedtick
  api.call({ 'read', n.id, n.kind }, nil, function(err, r)
    if err or not vim.api.nvim_buf_is_valid(buf) or n.saving then
      return
    end
    if vim.b[buf].changedtick ~= tick or vim.bo[buf].modified then
      return
    end
    n.base, n.hash = r.base, r.hash
    if r.text ~= M.buffer_text(buf) then
      M.set_text(buf, r.text)
      vim.bo[buf].modified = false
    end
  end)
end

local function create_then_save(buf, n)
  api.call({ 'create', n.path }, nil, function(err, e)
    if err then
      vim.bo[buf].modified = true
      return notify('could not make ' .. n.path .. ': ' .. err.msg, vim.log.levels.ERROR)
    end
    yana().refresh_tree()
    api.call({ 'read', e.id, e.kind }, nil, function(err2, r)
      if err2 then
        vim.bo[buf].modified = true
        return notify('could not save ' .. n.path .. ': ' .. err2.msg, vim.log.levels.ERROR)
      end
      n.id, n.kind, n.base, n.hash, n.role = r.id, r.kind, r.base, r.hash, r.role
      n.saving = false
      M.write_note(buf)
    end)
  end)
end

--- :w on a note. The buffer counts as written at once, so :wq quits
--- without waiting; the save itself runs in the background. A failure
--- marks the buffer modified again and says why, and quitting first
--- waits for saves in flight (init.lua, QuitPre), so a failed save stops
--- the quit instead of losing the text.
function M.write_note(buf)
  local n = M.notes[buf]
  if not n then
    return notify('this note has not loaded yet', vim.log.levels.WARN)
  end
  if n.role == 'viewer' then
    return notify(n.path .. ' is read-only for you', vim.log.levels.ERROR)
  end
  if n.saving then
    n.again = true
    vim.bo[buf].modified = false
    return
  end
  n.saving = true
  vim.bo[buf].modified = false
  if not n.id then
    return create_then_save(buf, n)
  end
  local text, tick = M.buffer_text(buf), vim.b[buf].changedtick
  local body = vim.json.encode({ kind = n.kind, text = text, base = n.base, hash = n.hash })
  api.call({ 'write', n.id }, { stdin = body }, function(err, r)
    n.saving = false
    if not vim.api.nvim_buf_is_valid(buf) then
      return
    end
    if err then
      n.again = false
      vim.bo[buf].modified = true
      return notify('could not save ' .. n.path .. ' (' .. err.msg .. '); the buffer keeps your text', vim.log.levels.ERROR)
    end
    local typed = vim.b[buf].changedtick ~= tick
    if n.kind == 'html' then
      n.hash = r.hash
      if r.conflict_copy and r.conflict_copy ~= '' then
        notify('the note changed elsewhere; your version is kept as ' .. r.conflict_copy, vim.log.levels.WARN)
      end
    else
      n.base = r.base
      if r.text ~= text then
        local merged = r.text
        if typed then
          -- Typed during the save: put those keystrokes on the merged text.
          merged = M.merge3(text, M.buffer_text(buf), r.text)
        end
        if merged then
          M.set_text(buf, merged)
          if not typed then
            vim.bo[buf].modified = false
          end
          local c = #hunks(nl(text), nl(r.text))
          notify(string.format('merged %d change%s from elsewhere into %s', c, c == 1 and '' or 's', n.path))
        else
          notify('edits from elsewhere overlap what you typed during the save; :w again keeps yours', vim.log.levels.WARN)
        end
      end
    end
    if n.again then
      n.again = false
      return M.write_note(buf)
    end
    vim.api.nvim_exec_autocmds('User', { pattern = 'YanaSaved', data = { buf = buf, path = n.path } })
  end)
end

--- The [[target]] under the cursor, if any.
function M.link_under_cursor()
  local line = vim.api.nvim_get_current_line()
  local col = vim.api.nvim_win_get_cursor(0)[2] + 1
  for s, inner, e in line:gmatch('()%[%[(.-)%]%]()') do
    if col >= s and col < e then
      return inner
    end
  end
end

function M.follow_link(buf, fallback)
  local target = M.link_under_cursor()
  local n = M.notes[buf]
  if not target or not n then
    return vim.cmd.normal({ args = { fallback }, bang = true })
  end
  api.call({ 'resolve', n.path, target }, nil, function(err, e)
    if err then
      return notify(err.msg, vim.log.levels.WARN)
    end
    yana().open_note(e.path)
  end)
end

function M.note_maps(buf)
  map(buf, 'note', 'gf', function()
    M.follow_link(buf, 'gf')
  end, 'follow the [[wikilink]] under the cursor')
  map(buf, 'note', '<CR>', function()
    M.follow_link(buf, vim.api.nvim_replace_termcodes('<CR>', true, false, true))
  end, 'follow the [[wikilink]] under the cursor')
  map(buf, 'note', '-', function()
    local path = (M.notes[buf] or {}).path or yana().parse(vim.api.nvim_buf_get_name(buf)).path
    local dir = path:match('^(.*)/[^/]*$') or ''
    yana().edit(yana().name(dir, true), path)
  end, 'the folder holding this note')
  map(buf, 'note', 'g?', function()
    M.show_help('note')
  end, 'this help')
end

return M
