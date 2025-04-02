package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type mcpSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &mcpSchema{}

func (s *mcpSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.Func("__mcp", s.mcp).
			Doc("instantiates an MCP with an environment").
			ArgDoc("env", "Environment to use"),
	}.Install(s.srv)
	dagql.Fields[*core.MCP]{
		dagql.NodeFunc("__serve", func(ctx context.Context, self dagql.Instance[*core.MCP], _ struct{}) (dagql.Nullable[core.Void], error) {
			return dagql.Null[core.Void](), self.Self.Serve(ctx, s.srv)
		}).
			Doc("serve MCP"),
	}.Install(s.srv)
}

func (s *mcpSchema) mcp(ctx context.Context, parent *core.Query, args struct {
	Env core.EnvID
}) (*core.MCP, error) {
	env, err := args.Env.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return core.NewMCP(parent, env.Self), nil
}
