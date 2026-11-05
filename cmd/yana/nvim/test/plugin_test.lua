-- The plugin's tests, run by TestNvimPlugin (cmd/yana/api_test.go):
--   nvim --headless -u NONE --cmd 'set rtp^=cmd/yana/nvim' -l cmd/yana/nvim/test/plugin_test.lua
-- against a real server, with YANA_BIN, YANA_CLI_SESSION (owner),
-- YANA_TEST_VIEWER (a viewer's session), YANA_TEST_SERVER and
-- YANA_TEST_TOKEN (the owner's access token, for edits "from elsewhere").
vim.cmd.runtime('plugin/yana.lua')
local yana = require('yana')
local buf = require('yana.buf')
local pick = require('yana.pick')

local server = assert(vim.env.YANA_TEST_SERVER)
local host = server:gsub('^https?://', '')
local failures = 0

local messages = {}
vim.notify = function(msg)
  messages[#messages + 1] = msg
end

local function test(name, f)
  local ok, err = xpcall(f, debug.traceback)
  print((ok and 'ok    ' or 'FAIL  ') .. name)
  if not ok then
    print(err)
    print('messages: ' .. vim.inspect(messages))
    failures = failures + 1
  end
end

local function eq(got, want, what)
  if not vim.deep_equal(got, want) then
    error((what or 'value') .. ': want ' .. vim.inspect(want) .. ', got ' .. vim.inspect(got), 2)
  end
end

local function wait(what, cond, ms)
  if not vim.wait(ms or 10000, cond, 20) then
    error('timed out waiting for ' .. what, 2)
  end
end

-- yana api, synchronously, for setting up.
local function sh(...)
  local r = vim.system({ vim.env.YANA_BIN, 'api', ... }, { text = true }):wait()
  assert(r.code == 0, 'yana api ' .. table.concat({ ... }, ' ') .. ': ' .. r.stdout .. r.stderr)
  return vim.json.decode(r.stdout)
end

local function put(id, text)
  local n = sh('read', id)
  local r = vim.system({ vim.env.YANA_BIN, 'api', 'write', id }, { stdin = vim.json.encode({ kind = 'md', text = text, base = n.base }), text = true }):wait()
  assert(r.code == 0, r.stdout)
end

-- An edit made in the app meanwhile: an append through the HTTP API.
local function append(id, content)
  local r = vim.system({
    'curl', '-sf', '-X', 'POST', server .. '/api/notes/' .. id .. '/append',
    '-H', 'Authorization: Bearer ' .. vim.env.YANA_TEST_TOKEN,
    '-H', 'Content-Type: application/json',
    '-d', vim.json.encode({ content = content }),
  }):wait()
  assert(r.code == 0, 'append failed')
end

local function refresh()
  local done = false
  yana.refresh_tree(function()
    done = true
  end)
  wait('the tree', function()
    return done
  end)
end

local function names(b)
  return vim.tbl_map(function(l)
    return (l:gsub('^/%d+ ', ''))
  end, vim.api.nvim_buf_get_lines(b, 0, -1, false))
end

local function text(b)
  return table.concat(vim.api.nvim_buf_get_lines(b or 0, 0, -1, false), '\n')
end

local function loaded(b)
  wait('the note to load', function()
    return vim.b[b].yana_loaded == true
  end)
end

-- --- pure logic ---------------------------------------------------------------------

test('sf: day words and dates put the daily note first', function()
  local now = os.time({ year = 2026, month = 11, day = 4, hour = 9 })
  local notes = {
    { path = 'home/kiln.md', tags = { 'pottery' } },
    { path = 'home/today-list.md' },
    { path = 'work/kiln-budget.md', tags = { 'money' } },
    { path = 'work/plan.md', tags = { 'pottery' } },
  }
  local got = pick.pick_notes('today', notes, now)
  eq(got[1].day, '2026-11-04', 'today')
  eq(got[2].note.path, 'home/today-list.md', 'notes follow the daily note')
  for q, day in pairs({ yesterday = '2026-11-03', tom = '2026-11-05', ['2026-01-31'] = '2026-01-31' }) do
    eq(pick.pick_notes(q, notes, now)[1].day, day, q)
  end
  eq(pick.pick_notes('to', notes, now)[1].day, nil, 'two letters are not a day')
  eq(pick.date_for('2026-02-30', now), nil, 'not a date')
  local paths = function(items)
    return vim.tbl_map(function(it)
      return it.note.path
    end, items)
  end
  eq(paths(pick.pick_notes('#pot', notes, now)), { 'home/kiln.md', 'work/plan.md' }, '#tag')
  eq(paths(pick.pick_notes('work/kiln', notes, now)), { 'work/kiln-budget.md' }, 'space/')
  eq(paths(pick.pick_notes('work/ #pottery', notes, now)), { 'work/plan.md' }, 'space/ #tag')
  eq(paths(pick.pick_notes('#nothing', notes, now)), {}, 'unknown tag')
end)

test('fuzzy ranking and the matched line of a search hit', function()
  local notes = {
    { path = 'home/r/e/c/i/p/e.md' },
    { path = 'work/specs/recipe-engine.md' },
    { path = 'home/recipes.md', title = 'Recipes' },
    { path = 'home/journal.md', title = 'Jam and bread' },
  }
  local got = pick.rank('recipe', notes)
  eq(#got, 3, 'recipe matches')
  eq(got[1].path, 'home/recipes.md', 'best')
  eq(pick.rank('jam', notes)[1].path, 'home/journal.md', 'by title')
  local t = '# Kiln\n\nfiring schedule\ncone 6 glaze\n'
  eq(pick.match_line(t, '…cone 6 <mark>glaze</mark>…', 'glaze'), 4, 'marked term')
  eq(pick.match_line(t, '', 'Firing'), 3, 'query word')
end)

test('three-way merge of typing during a save', function()
  eq(buf.merge3('a\nb\nc\n', 'a\nB\nc\n', 'a\nb\nc\nd\n'), 'a\nB\nc\nd\n', 'apart')
  eq(buf.merge3('a\nb\n', 'a\nX\n', 'a\nY\n'), nil, 'same line')
  eq(buf.merge3('a\n', 'z\na\n', 'a\nq\n'), 'z\na\nq\n', 'both ends')
end)

-- --- against the server -------------------------------------------------------------

yana.setup({})
local a = sh('create', 'home/lua/a')
sh('create', 'home/lua/b')
sh('create', 'home/lua/sub/')

test('a folder buffer lists the folder, ids concealed', function()
  vim.cmd.edit('yana://' .. host .. '/home/lua/')
  local b = vim.api.nvim_get_current_buf()
  wait('the listing', function()
    return #vim.api.nvim_buf_get_lines(b, 0, -1, false) == 3 and not vim.bo[b].modified
  end)
  eq(names(b), { 'sub/', 'a.md', 'b.md' }, 'listing')
  eq(vim.bo[b].filetype, 'yana', 'filetype')
  local marks = vim.api.nvim_buf_get_extmarks(b, -1, 0, -1, { details = true })
  eq(#marks, 3, 'one concealed id per line')
  eq(marks[1][4].conceal, '', 'concealed')
  -- The spaces at the top.
  vim.cmd.edit('yana://' .. host .. '/')
  wait('the spaces', function()
    return vim.tbl_contains(names(0), 'home/')
  end)
  assert(vim.tbl_contains(names(0), 'shared/'), 'shared is listed')
  vim.cmd.buffer(b)
end)

test('editing the listing renames, creates and deletes after confirming', function()
  local b = vim.api.nvim_get_current_buf()
  local lines = vim.api.nvim_buf_get_lines(b, 0, -1, false)
  local asked
  -- Declined: nothing happens, the edits stay.
  yana.confirm = function(l)
    asked = l
    return false
  end
  vim.api.nvim_buf_set_lines(b, 2, 3, false, {}) -- delete b.md
  vim.cmd.write()
  eq(asked, { 'DELETE  home/lua/b.md  (to the trash)' }, 'asked')
  assert(vim.bo[b].modified, 'declined edits stay')
  -- Confirmed: all of it.
  vim.api.nvim_buf_set_lines(b, 0, -1, false, { lines[1], (lines[2]:gsub('a%.md$', 'a2.md')), 'c', 'newdir/' })
  yana.confirm = function(l)
    asked = l
    return true
  end
  vim.cmd.write()
  table.sort(asked)
  eq(asked, {
    'CREATE  home/lua/c',
    'DELETE  home/lua/b.md  (to the trash)',
    'MKDIR   home/lua/newdir/',
    'RENAME  a.md  →  home/lua/a2.md',
  }, 'the actions')
  wait('the new listing', function()
    return vim.deep_equal(names(b), { 'newdir/', 'sub/', 'a2.md', 'c.md' }) and not vim.bo[b].modified
  end)
  local tree = sh('tree')
  local paths = {}
  for _, e in ipairs(tree.entries) do
    paths[e.path] = e.id or true
  end
  assert(paths['home/lua/a2.md'] == a.id, 'renamed, same id')
  assert(not paths['home/lua/b.md'], 'deleted')
  assert(paths['home/lua/c.md'] and paths['home/lua/newdir'], 'created')
end)

test(':w on a note merges an append made meanwhile', function()
  put(a.id, '# A\nbody\n')
  vim.cmd.edit(vim.fn.fnameescape('yana://' .. host .. '/home/lua/a2.md'))
  local b = vim.api.nvim_get_current_buf()
  loaded(b)
  eq(vim.bo[b].filetype, 'markdown', 'filetype')
  eq(text(b), '# A\nbody', 'read')
  vim.api.nvim_buf_set_lines(b, 0, 1, false, { '# A, mine' })
  append(a.id, '- theirs')
  messages = {}
  vim.cmd.write()
  wait('the merged text', function()
    return text(b):find('- theirs', 1, true) ~= nil and not buf.notes[b].saving
  end)
  eq(text(b), '# A, mine\nbody\n- theirs', 'buffer holds the merge')
  assert(not vim.bo[b].modified, 'saved')
  assert(table.concat(messages, '\n'):find('merged 1 change from elsewhere', 1, true), 'said so: ' .. vim.inspect(messages))
  eq(sh('read', a.id).text, '# A, mine\nbody\n- theirs\n', 'on the server')
end)

test('a failed save keeps the text and the buffer modified', function()
  local b = vim.api.nvim_get_current_buf()
  local id = buf.notes[b].id
  buf.notes[b].id = '01AAAAAAAAAAAAAAAAAAAAAAAA'
  vim.api.nvim_buf_set_lines(b, -1, -1, false, { 'unsaved' })
  messages = {}
  vim.cmd.write()
  wait('the failure', function()
    return vim.bo[b].modified and not buf.notes[b].saving
  end)
  buf.notes[b].id = id
  assert(text(b):find('unsaved', 1, true), 'text kept')
  assert(table.concat(messages):find('could not save', 1, true), 'said why: ' .. vim.inspect(messages))
  vim.api.nvim_buf_set_lines(b, -2, -1, false, {})
  vim.cmd.write()
  wait('the save', function()
    return not vim.bo[b].modified and not buf.notes[b].saving
  end)
end)

test('an unmodified note follows edits made elsewhere', function()
  local b = vim.api.nvim_get_current_buf()
  append(a.id, '- live')
  wait('the live refresh', function()
    return text(b):find('- live', 1, true) ~= nil
  end)
  assert(not vim.bo[b].modified, 'still unmodified')
end)

test('a new note is made by its first :w', function()
  vim.cmd.edit(vim.fn.fnameescape('yana://' .. host .. '/home/lua/fresh.md'))
  local b = vim.api.nvim_get_current_buf()
  wait('the new buffer', function()
    return buf.notes[b] ~= nil
  end)
  vim.api.nvim_buf_set_lines(b, 0, -1, false, { 'fresh text' })
  vim.cmd.write()
  wait('the save', function()
    return buf.notes[b].id ~= nil and not buf.notes[b].saving
  end)
  eq(sh('read', buf.notes[b].id).text, 'fresh text\n', 'on the server')
end)

test('a viewer gets a read-only buffer', function()
  local owner = vim.env.YANA_CLI_SESSION
  vim.env.YANA_CLI_SESSION = vim.env.YANA_TEST_VIEWER
  vim.cmd.edit('yana://' .. host .. '/shared/readme.md')
  local b = vim.api.nvim_get_current_buf()
  loaded(b)
  vim.env.YANA_CLI_SESSION = owner
  eq(text(b), 'Read only.', 'text')
  assert(vim.bo[b].readonly and not vim.bo[b].modifiable, 'read-only')
end)

test('gf on a [[wikilink]] opens the note it names', function()
  local target = sh('create', 'home/lua/target note')
  local linker = sh('create', 'home/lua/linker')
  put(linker.id, 'first line\nsee [[target note|the target]] here\n')
  refresh()
  vim.cmd.edit('yana://' .. host .. '/home/lua/linker.md')
  local b = vim.api.nvim_get_current_buf()
  loaded(b)
  vim.api.nvim_win_set_cursor(0, { 2, 8 })
  vim.cmd.normal('gf')
  wait('the target', function()
    return vim.api.nvim_buf_get_name(0):find('target note.md', 1, true) ~= nil
  end)
  assert(target.id, 'made')
end)

test(':Yana today opens the daily note, made when missing', function()
  vim.cmd('Yana today')
  wait('the daily note', function()
    return vim.api.nvim_buf_get_name(0):find(os.date('%Y-%m-%d'), 1, true) ~= nil
  end)
end)

test('the next launch reopens the open buffers, where they were', function()
  vim.cmd.edit(vim.fn.fnameescape('yana://' .. host .. '/home/lua/linker.md'))
  local b = vim.api.nvim_get_current_buf()
  loaded(b)
  vim.api.nvim_win_set_cursor(0, { 2, 4 })
  local before = {}
  for _, x in ipairs(vim.api.nvim_list_bufs()) do
    if vim.bo[x].buflisted and vim.api.nvim_buf_get_name(x):find('^yana://') then
      before[#before + 1] = vim.api.nvim_buf_get_name(x)
    end
  end
  yana.save_state()
  -- A fresh start: no yana buffers.
  vim.cmd.enew()
  for _, x in ipairs(vim.api.nvim_list_bufs()) do
    if vim.api.nvim_buf_get_name(x):find('^yana://') then
      vim.api.nvim_buf_delete(x, { force = true })
    end
  end
  buf.notes, buf.folders = {}, {}
  yana.launch({ server = server, host = host })
  local after = {}
  for _, x in ipairs(vim.api.nvim_list_bufs()) do
    if vim.bo[x].buflisted and vim.api.nvim_buf_get_name(x):find('^yana://') then
      after[#after + 1] = vim.api.nvim_buf_get_name(x)
    end
  end
  eq(after, before, 'the same buffers, in order')
  for _, x in ipairs(vim.api.nvim_list_bufs()) do
    assert(not (vim.bo[x].buflisted and vim.api.nvim_buf_get_name(x) == ''), 'the empty start buffer is gone')
  end
  eq(vim.api.nvim_buf_get_name(0), 'yana://' .. host .. '/home/lua/linker.md', 'the current one')
  loaded(vim.api.nvim_get_current_buf())
  eq(vim.api.nvim_win_get_cursor(0), { 2, 4 }, 'the cursor')
  -- A note named by the shortcut opens too, and is current.
  yana.launch({ server = server, host = host, open = 'home/lua/a2.md' })
  eq(vim.api.nvim_buf_get_name(0), 'yana://' .. host .. '/home/lua/a2.md', 'opened note is current')
end)

if failures > 0 then
  print(failures .. ' failed')
  vim.cmd.cquit(1)
end
print('all passed')
vim.cmd.qall({ bang = true })
