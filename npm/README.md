# terrarium-mcp

Real, disposable VirtualBox VMs from the CLI or over MCP, for you and your AI
agent. This package bundles the Windows binary of
[terrarium](https://github.com/chryaner/terrarium) so nothing has to be
installed first.

Requires a Windows host with [VirtualBox](https://www.virtualbox.org/) 7.x.

Give your agent real machines (Claude Code):

```
claude mcp add terrarium -- npx -y terrarium-mcp mcp
```

Use the CLI:

```
npx -y terrarium-mcp doctor
npx -y terrarium-mcp fork debian-12 t1
```

Or install it globally so `terrarium` is on your PATH:

```
npm i -g terrarium-mcp
```

Docs, recipes and the demos are in the
[repository](https://github.com/chryaner/terrarium).
