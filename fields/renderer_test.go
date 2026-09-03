package fields

import (
	apidsl "github.com/wow-look-at-my/api-dsl"
)

// testRenderer is the renderer the package's own tests evaluate Expr and Footer
// templates with. It is the shared api-dsl renderer without the helpers a CLI
// adds on top, which is what an importing program gets by default.
var testRenderer Renderer = apidsl.NewRenderer(nil).Render
