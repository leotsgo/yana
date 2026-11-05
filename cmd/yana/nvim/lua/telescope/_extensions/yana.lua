-- :Telescope yana find|grep|recent
local pick = require('yana.pick')

return require('telescope').register_extension({
  exports = {
    yana = function(opts)
      pick.find((opts or {}).query)
    end,
    find = function(opts)
      pick.find((opts or {}).query)
    end,
    grep = function(opts)
      pick.grep((opts or {}).query)
    end,
    recent = function(opts)
      pick.recent((opts or {}).query)
    end,
  },
})
