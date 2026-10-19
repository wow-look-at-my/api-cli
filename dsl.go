package main

import apidsl "github.com/wow-look-at-my/api-dsl"

// The XML-with-placeholders language lives in api-dsl.
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
