package main

import apidsl "github.com/wow-look-at-my/api-dsl"

// The XML-with-placeholders language lives in api-dsl. This file is the local
// boundary: it gives the shared API the names the rest of this package calls,
// so the import stays in one place and a call site stays short.
//
// One shape differs from a plain rename: the element name is the method
// Name(), never a field.
type xnode = apidsl.Node

var (
	parseDOM        = apidsl.ParseDOM
	checkAttrs      = apidsl.CheckAttrs
	compileContent  = apidsl.CompileContent
	compileTextElem = apidsl.CompileTextElem
	textOf          = apidsl.TextOf
	isPlaceholder   = apidsl.IsPlaceholder
	envMap          = apidsl.EnvMap
	lookupPath      = apidsl.LookupPath
	mergeVars       = apidsl.MergeVars
	isTruthy        = apidsl.IsTruthy
	templateTruthy  = apidsl.Truthy
)
