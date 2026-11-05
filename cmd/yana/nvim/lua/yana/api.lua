-- The plugin's only way to the server: `yana api <verb>`, run async.
-- The binary holds the session; no token ever reaches Lua.
local M = {}

function M.bin()
  return vim.env.YANA_BIN or 'yana'
end

local function decode(s)
  local ok, v = pcall(vim.json.decode, s, { luanil = { object = true, array = true } })
  if ok then
    return v
  end
end

--- Run `yana api <args>`; cb(err, value) on the main loop. err is
--- { msg, status }. With opts.lines, value is the list of JSON lines.
---@param args string[]
---@param opts? { stdin?: string, lines?: boolean }
---@param cb fun(err: table|nil, value: any)
function M.call(args, opts, cb)
  opts = opts or {}
  local cmd = { M.bin(), 'api' }
  vim.list_extend(cmd, args)
  local ok, err = pcall(vim.system, cmd, { stdin = opts.stdin, text = true }, function(r)
    vim.schedule(function()
      local out = r.stdout or ''
      if r.code ~= 0 then
        local v = decode(vim.trim(out)) or {}
        local msg = v.error or vim.trim(r.stderr or '')
        if msg == '' then
          msg = 'yana api ' .. args[1] .. ' failed'
        end
        return cb({ msg = msg, status = v.status or 0 })
      end
      if opts.lines then
        local list = {}
        for line in out:gmatch('[^\n]+') do
          list[#list + 1] = decode(line)
        end
        return cb(nil, list)
      end
      local v = decode(out)
      if v == nil then
        return cb({ msg = 'yana api ' .. args[1] .. ': unreadable answer', status = 0 })
      end
      cb(nil, v)
    end)
  end)
  if not ok then
    vim.schedule(function()
      cb({ msg = 'cannot run ' .. M.bin() .. ': ' .. tostring(err), status = 0 })
    end)
  end
end

--- Start `yana api watch <spaces>`; on_line(value) per JSON line. The
--- process ends when the handle is killed or neovim exits (stdin closes).
function M.watch(spaces, on_line)
  local cmd = { M.bin(), 'api', 'watch' }
  vim.list_extend(cmd, spaces)
  local pending = ''
  local ok, h = pcall(vim.system, cmd, {
    stdin = true,
    text = true,
    stdout = function(_, data)
      if not data then
        return
      end
      pending = pending .. data
      while true do
        local line, rest = pending:match('^([^\n]*)\n(.*)$')
        if not line then
          break
        end
        pending = rest
        local v = decode(line)
        if v then
          vim.schedule(function()
            on_line(v)
          end)
        end
      end
    end,
  })
  if ok then
    return h
  end
end

return M
