package designopts

import (
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
)

// ProtoOptions returns the proto compiler options cfg sets, with include roots
// made absolute against projectRoot; PBDir is empty when `output.pb` is "-".
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
		opts.Includes = append(opts.Includes, filepath.Join(projectRoot, filepath.FromSlash(inc)))
	}
	return opts
}
