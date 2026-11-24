package web

import "embed"

//go:embed templates/*.html static/*
var embeddedFS embed.FS
