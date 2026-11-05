-- yana: notes from a yana server in yana:// buffers. See :Yana help.
if vim.g.loaded_yana then
  return
end
vim.g.loaded_yana = true

local group = vim.api.nvim_create_augroup('yana', { clear = true })

vim.api.nvim_create_autocmd('BufReadCmd', {
  group = group,
  pattern = 'yana://*',
  desc = 'yana: read a folder listing or a note from the server',
  callback = function(ev)
    local yana = require('yana')
    local p = yana.parse(ev.match)
    if not p then
      return
    end
    if p.dir then
      require('yana.buf').read_folder(ev.buf, p.path)
    else
      require('yana.buf').read_note(ev.buf, p.path)
    end
  end,
})

vim.api.nvim_create_autocmd('BufWriteCmd', {
  group = group,
  pattern = 'yana://*',
  desc = 'yana: apply a folder listing, or save a note through the merge',
  callback = function(ev)
    local b = require('yana.buf')
    if b.folders[ev.buf] then
      b.write_folder(ev.buf)
    else
      b.write_note(ev.buf)
    end
  end,
})

vim.api.nvim_create_user_command('Yana', function(o)
  require('yana').command(o)
end, {
  nargs = '*',
  desc = 'yana: notes on the signed-in server',
  complete = function(lead, line)
    return require('yana').complete(lead, line)
  end,
})
