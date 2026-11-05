-- The pickers: notes by name (sf), full-text search (sg), recent notes.
-- The matching is plain Lua over the cached tree, so typing never waits
-- on the network; telescope shows it when present, vim.ui.select if not.
local M = {}

local function lower(s)
  return (s or ''):lower()
end

--- How well query's characters match s in order, ignoring case: runs of
--- consecutive characters and starts of words count more, and a plain
--- substring, above all one in the file name, beats any scattered match.
--- nil when they do not match.
function M.fuzzy_score(query, s)
  local q, r = lower(query), lower(s)
  if q == '' then
    return 0
  end
  local best
  for start = 1, #r do
    if r:sub(start, start) == q:sub(1, 1) then
      local score, qi, prev = 0, 1, start - 2
      for i = start, #r do
        if qi > #q then
          break
        end
        if r:sub(i, i) == q:sub(qi, qi) then
          score = score + 1
          if i == prev + 1 then
            score = score + 5
          end
          if i == 1 or ('/-_ .'):find(r:sub(i - 1, i - 1), 1, true) then
            score = score + 3
          end
          prev, qi = i, qi + 1
        end
      end
      if qi > #q and (not best or score > best) then
        best = score
      end
    end
  end
  if not best then
    return nil
  end
  if r:find(q, 1, true) then
    best = best + 20
    if (r:match('[^/]*$') or ''):find(q, 1, true) then
      best = best + 10
    end
  end
  return best
end

--- The notes matching query by path or title, best first; an empty
--- query keeps them all in their order.
function M.rank(query, notes)
  if vim.trim(query or '') == '' then
    return notes
  end
  local hits = {}
  for i, e in ipairs(notes) do
    local sp, st = M.fuzzy_score(query, e.path), M.fuzzy_score(query, e.title)
    if sp or st then
      hits[#hits + 1] = { e = e, score = math.max(sp or 0, st or 0), i = i }
    end
  end
  table.sort(hits, function(a, b)
    if a.score ~= b.score then
      return a.score > b.score
    end
    if #a.e.path ~= #b.e.path then
      return #a.e.path < #b.e.path
    end
    return a.i < b.i
  end)
  return vim.tbl_map(function(h)
    return h.e
  end, hits)
end

--- A word as a day, YYYY-MM-DD: today, yesterday and tomorrow (from
--- their first three letters on) or a YYYY-MM-DD date. now is os.time().
function M.date_for(word, now)
  local w = lower(vim.trim(word or ''))
  if #w >= 3 then
    for name, days in pairs({ today = 0, yesterday = -1, tomorrow = 1 }) do
      if name:sub(1, #w) == w then
        local t = os.date('*t', now)
        return os.date('%Y-%m-%d', os.time({ year = t.year, month = t.month, day = t.day + days, hour = 12 }))
      end
    end
  end
  local y, m, d = w:match('^(%d%d%d%d)%-(%d%d)%-(%d%d)$')
  if y then
    local t = os.time({ year = tonumber(y), month = tonumber(m), day = tonumber(d), hour = 12 })
    if os.date('%Y-%m-%d', t) == w then
      return w
    end
  end
end

--- The find-notes picker's items for query. A day name or date puts that
--- daily note first ({ day = 'YYYY-MM-DD' }, made when opened); #tag
--- keeps notes with a tag starting so; a leading space/ keeps that
--- space's notes; the rest of the query is fuzzy. Notes are { note = e }.
function M.pick_notes(query, notes, now)
  local items = {}
  local day = M.date_for(query, now)
  if day then
    items[1] = { day = day }
  end
  local words, keep = {}, notes
  for i, w in ipairs(vim.split(vim.trim(query or ''), '%s+', { trimempty = true })) do
    if w:sub(1, 1) == '#' and #w > 1 then
      local tag = lower(w:sub(2))
      keep = vim.tbl_filter(function(e)
        for _, t in ipairs(e.tags or {}) do
          if lower(t):sub(1, #tag) == tag then
            return true
          end
        end
        return false
      end, keep)
    else
      local sp, rest = w:match('^([^/]+)/(.*)$')
      local narrowed = sp
        and i == 1
        and vim.tbl_filter(function(e)
          return vim.startswith(e.path, sp .. '/')
        end, keep)
      if narrowed and #narrowed > 0 then
        keep = narrowed
        if rest ~= '' then
          words[#words + 1] = rest
        end
      else
        words[#words + 1] = w
      end
    end
  end
  for _, e in ipairs(M.rank(table.concat(words, ' '), keep)) do
    items[#items + 1] = { note = e }
  end
  return items
end

--- The 1-based line of text a search hit points at: the first line with
--- a term the server marked in the snippet, else one of the query's
--- words; nil when none is found.
function M.match_line(text, snippet, query)
  local terms = {}
  for t in (snippet or ''):gmatch('<mark>(.-)</mark>') do
    terms[#terms + 1] = t
  end
  vim.list_extend(terms, vim.split(query or '', '%s+', { trimempty = true }))
  local lines = vim.split(lower(text), '\n', { plain = true })
  for _, t in ipairs(terms) do
    t = lower(t):gsub('["*()]', '')
    if t ~= '' then
      for i, l in ipairs(lines) do
        if l:find(t, 1, true) then
          return i
        end
      end
    end
  end
end

-- --- the pickers ----------------------------------------------------------

local function yana()
  return require('yana')
end

local function telescope()
  local want = yana().config.picker
  if want == 'select' then
    return nil
  end
  local ok = pcall(require, 'telescope')
  if ok then
    return true
  end
  if want == 'telescope' then
    vim.notify('yana: telescope is not installed; using vim.ui.select', vim.log.levels.WARN)
  end
end

local function item_label(it)
  if it.day then
    return 'daily note ' .. it.day .. ' (today, yesterday, tomorrow or a date)'
  end
  local e = it.note
  local label = e.path
  if e.title and e.title ~= '' and e.title ~= vim.fn.fnamemodify(e.path, ':t:r') then
    label = label .. '  — ' .. e.title
  end
  return label
end

--- Open what a picker chose: a note (at line, when given), or a daily
--- note, made when missing.
local function choose(it, line)
  if it.day then
    return yana().open_day(it.day)
  end
  yana().open_note(it.note.path, line)
end

-- A telescope previewer showing the note's text, fetched once per note.
local texts = {}
local function previewer()
  local previewers = require('telescope.previewers')
  return previewers.new_buffer_previewer({
    title = 'Note',
    define_preview = function(self, entry)
      local it = entry.value
      local e = it.note
      local buf = self.state.bufnr
      if not e then
        vim.api.nvim_buf_set_lines(buf, 0, -1, false, { 'Opening makes ' .. it.day .. "'s daily note when it is missing." })
        return
      end
      local function show(text)
        if vim.api.nvim_buf_is_valid(buf) then
          vim.api.nvim_buf_set_lines(buf, 0, -1, false, vim.split(text, '\n', { plain = true }))
          vim.bo[buf].filetype = e.kind == 'html' and 'html' or 'markdown'
        end
      end
      if texts[e.id] then
        return show(texts[e.id])
      end
      if e.kind ~= 'md' and e.kind ~= 'html' then
        return show('(a ' .. tostring(e.kind) .. '; opening it opens the app)')
      end
      show('loading…')
      require('yana.api').call({ 'read', e.id, e.kind }, nil, function(err, n)
        texts[e.id] = err and ('(' .. err.msg .. ')') or n.text
        show(texts[e.id])
      end)
    end,
  })
end

local function telescope_pick(title, finder, sorter, query, on_choose, with_preview)
  local pickers = require('telescope.pickers')
  local actions = require('telescope.actions')
  local action_state = require('telescope.actions.state')
  pickers
    .new({}, {
      prompt_title = title,
      default_text = query,
      finder = finder,
      sorter = sorter,
      previewer = with_preview and previewer() or nil,
      debounce = 100,
      attach_mappings = function(prompt)
        actions.select_default:replace(function()
          local entry = action_state.get_selected_entry()
          local text = action_state.get_current_line()
          actions.close(prompt)
          if entry then
            on_choose(entry.value, text)
          end
        end)
        return true
      end,
    })
    :find()
end

-- A sorter that keeps the finder's own order.
local function keep_order()
  local sorters = require('telescope.sorters')
  return sorters.Sorter:new({
    scoring_function = function(_, _, _, entry)
      return entry.index or 1
    end,
    highlighter = function()
      return {}
    end,
  })
end

local function select_pick(prompt, items, on_choose)
  if #items == 0 then
    return vim.notify('yana: nothing found', vim.log.levels.INFO)
  end
  vim.ui.select(items, { prompt = prompt, format_item = item_label }, function(it)
    if it then
      on_choose(it)
    end
  end)
end

--- <leader>sf: notes by path or title.
function M.find(query)
  yana().with_tree(function(notes)
    if telescope() then
      local finders = require('telescope.finders')
      telescope_pick(
        'yana notes  (today · #tag · space/)',
        finders.new_dynamic({
          fn = function(prompt)
            local items = M.pick_notes(prompt, notes, os.time())
            for i, it in ipairs(items) do
              it.index = i
            end
            return items
          end,
          entry_maker = function(it)
            local label = item_label(it)
            return { value = it, display = label, ordinal = label, index = it.index }
          end,
        }),
        keep_order(),
        query or '',
        function(it)
          choose(it)
        end,
        true
      )
      return
    end
    vim.ui.input({ prompt = 'yana notes: ', default = query or '' }, function(q)
      if q then
        select_pick('yana notes', M.pick_notes(q, notes, os.time()), choose)
      end
    end)
  end, true)
end

--- <leader>sg: full-text search on the server, opening at the matched line.
function M.grep(query)
  local api = require('yana.api')
  local function open_hit(hit, q)
    require('yana').open_note(hit.path, function(text)
      return M.match_line(text, hit.snippet, q)
    end)
  end
  if telescope() then
    local finders = require('telescope.finders')
    local sorters = require('telescope.sorters')
    local last = ''
    telescope_pick(
      'yana search',
      finders.new_async_job({
        command_generator = function(prompt)
          last = prompt or ''
          if #vim.trim(last) < 2 then
            return nil
          end
          return { api.bin(), 'api', 'search', last }
        end,
        entry_maker = function(line)
          local ok, hit = pcall(vim.json.decode, line)
          if not ok or type(hit) ~= 'table' or hit.error then
            return nil
          end
          local snippet = (hit.snippet or ''):gsub('</?mark>', ''):gsub('%s+', ' ')
          local label = hit.path .. '  ' .. snippet
          hit.note = { id = hit.id, path = hit.path, kind = hit.kind, title = hit.title }
          return { value = hit, display = label, ordinal = label }
        end,
      }),
      sorters.highlighter_only({}),
      query or '',
      function(hit)
        open_hit(hit, last)
      end,
      true
    )
    return
  end
  vim.ui.input({ prompt = 'yana search: ', default = query or '' }, function(q)
    if not q or vim.trim(q) == '' then
      return
    end
    api.call({ 'search', q }, { lines = true }, function(err, hits)
      if err then
        return vim.notify('yana: ' .. err.msg, vim.log.levels.ERROR)
      end
      local items = vim.tbl_map(function(h)
        return { note = { id = h.id, path = h.path, title = h.title, kind = h.kind }, hit = h }
      end, hits)
      select_pick('yana search', items, function(it)
        open_hit(it.hit, q)
      end)
    end)
  end)
end

--- <leader><leader>: recently opened notes.
function M.recent(query)
  require('yana.api').call({ 'recent' }, nil, function(err, res)
    if err then
      return vim.notify('yana: ' .. err.msg, vim.log.levels.ERROR)
    end
    yana().with_tree(function()
      local items = {}
      for _, id in ipairs(res.ids) do
        local e = yana().state.by_id[id]
        if e then
          items[#items + 1] = { note = e }
        end
      end
      if telescope() then
        local finders = require('telescope.finders')
        local conf = require('telescope.config').values
        telescope_pick(
          'yana recent',
          finders.new_table({
            results = items,
            entry_maker = function(it)
              return { value = it, display = item_label(it), ordinal = it.note.path .. ' ' .. (it.note.title or '') }
            end,
          }),
          conf.generic_sorter({}),
          query or '',
          function(it)
            choose(it)
          end,
          true
        )
        return
      end
      select_pick('yana recent', items, choose)
    end)
  end)
end

return M
