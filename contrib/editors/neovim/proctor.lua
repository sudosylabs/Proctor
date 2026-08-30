-- Neovim 0.11+ and nvim-dap example for a Proctor checkout.
-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Copy the relevant parts into your own Neovim configuration; do not load
-- unreviewed repository-local Lua automatically.

local root = vim.fs.root(0, { "go.work" })
if root == nil then
  return
end
root = vim.fs.normalize(root)

local bin = root .. "/.build/bin"
local go_work = table.concat(vim.fn.readfile(root .. "/go.work"), "\n")
local go_version = go_work:match("^go%s+([%d.]+)")
if go_version == nil then
  error("Proctor go.work does not declare a Go version")
end

vim.lsp.config("proctor_gopls", {
  cmd = { bin .. "/gopls" },
  filetypes = { "go", "gomod", "gowork", "gotmpl" },
  cmd_env = {
    GOFLAGS = "-buildvcs=false -mod=readonly",
    GOTOOLCHAIN = "go" .. go_version,
    GOWORK = root .. "/go.work",
  },
  root_dir = function(bufnr, on_dir)
    local buffer_root = vim.fs.root(bufnr, { "go.work" })
    if buffer_root ~= nil and vim.fs.normalize(buffer_root) == root then
      on_dir(root)
    end
  end,
})
vim.lsp.enable("proctor_gopls")

local dap = require("dap")

-- Start `make debug-server-dap` in another terminal before this configuration.
-- The Make target owns local dependencies, generated secrets, and environment.
dap.adapters.proctor = {
  type = "server",
  host = "127.0.0.1",
  port = 2345,
}

dap.configurations.go = dap.configurations.go or {}
table.insert(dap.configurations.go, {
  name = "Proctor server (repository DAP)",
  type = "proctor",
  request = "launch",
  mode = "debug",
  program = root .. "/server/cmd/proctor",
  cwd = root,
  output = root .. "/.build/dev/diagnostics/server-dap",
  args = {
    "serve",
    "--config",
    root .. "/.build/dev/config/local.json",
  },
})
