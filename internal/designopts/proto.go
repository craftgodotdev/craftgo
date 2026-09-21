package designopts

import (
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
)

// ProtoOptions is what the proto compiler needs from the manifest: the
// module prefix, the pb directory (empty when `output.pb` is "-", which
// runs no plugin), the include roots made absolute against projectRoot,
// the file case the scaffold names follow, and the plugin commands.
func ProtoOptions(cfg *config.Config, projectRoot string) protodesign.Options {
	opts := protodesign.Options{
		Module:       cfg.Package,
		FileCase:     cfg.Output.FileCase,
		PluginGo:     cfg.Proto.Plugins.Go,
		PluginGoGrpc: cfg.Proto.Plugins.GoGrpc,
	}
	if !cfg.Output.PBDisabled() {
		opts.PBDir = cfg.Output.PB
	}
	for _, inc := range cfg.Proto.Includes {
		opts.Includes = append(opts.Includes, filepath.Join(projectRoot, filepath.FromSlash(inc)))
	}
	return opts
}
