package towline

import "embed"

//go:embed all:templates
var EmbeddedTemplates embed.FS

//go:embed skills/*
var EmbeddedSkills embed.FS
