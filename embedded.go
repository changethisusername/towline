package towline

import "embed"

//go:embed templates/* templates/packs/*/* templates/compose/*
var EmbeddedTemplates embed.FS

//go:embed skills/*
var EmbeddedSkills embed.FS
