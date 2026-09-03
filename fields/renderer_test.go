package fields

import (
	apidsl "github.com/wow-look-at-my/api-dsl"
)

// testRenderer evaluates Expr and Footer here: the bare api-dsl renderer, which
// is what an importing program gets before it adds helpers.
var testRenderer Renderer = func(tmpl string, data map[string]any) (string, error) {
	return apidsl.NewRenderer(nil).Render(tmpl, data)
}
