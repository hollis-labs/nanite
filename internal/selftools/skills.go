package selftools

import (
	"context"

	"github.com/hollis-labs/nanite/internal/skills"
	"github.com/hollis-labs/nanite/internal/tools"
)

func Skills(catalog skills.Catalog) []tools.Tool {
	return []tools.Tool{
		spec("skill_list", "List skill names and descriptions.", `{}`, nil, true,
			func(context.Context, struct{}) (string, error) { return catalog.Listing(), nil }),
		spec("skill_get", "Read one skill's instructions and directory.", `{"name":{"type":"string"}}`, []string{"name"}, true,
			func(_ context.Context, args struct{ Name string }) (string, error) { return catalog.Read(args.Name) }),
	}
}
