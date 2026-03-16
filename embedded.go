package towline

import "embed"

//go:embed templates/*
var EmbeddedTemplates embed.FS

//go:embed skills/*
var EmbeddedSkills embed.FS
