package designopts

import (
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
)

// ProtoOptions returns the proto compiler options cfg sets, with relative
// include roots made absolute against projectRoot; PBDir is empty when
// `output.pb` is "-".
func ProtoOptions(cfg *config.Config, projectRoot string) protodesign.Options {
	opts := protodesign.Options{
		Module:       cfg.Package,
		FileCase:     cfg.Output.FileCase,
		PluginGo:     cfg.Proto.Plugins.Go,
		PluginGoGRPC: cfg.Proto.Plugins.GoGRPC,
	}
	if !cfg.Output.PBDisabled() {
		opts.PBDir = cfg.Output.PB
	}
	for _, inc := range cfg.Proto.Includes {
		inc = filepath.FromSlash(inc)
		if !filepath.IsAbs(inc) {
			inc = filepath.Join(projectRoot, inc)
		}
		opts.Includes = append(opts.Includes, inc)
	}
	return opts
}
