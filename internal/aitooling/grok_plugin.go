package aitooling

import (
	"context"
	"encoding/json"
	"strings"
)

type grokPlugin struct {
	Name, Version, Source, Path string
	Enabled, Trusted            *bool
}

func (e *Engine) grokInventory(ctx context.Context, bin string) ([]grokPlugin, error) {
	out, err := e.run(ctx, bin, "plugin", "list", "--json")
	if err != nil {
		return nil, err
	}
	var plugins []grokPlugin
	err = json.Unmarshal([]byte(out), &plugins)
	return plugins, err
}
func (e *Engine) grokPonytail(ctx context.Context, pin string, op Operation) ItemResult {
	r := ItemResult{ID: "ponytail/grok", Kind: "integration"}
	bin := e.find("grok")
	if bin == "" {
		r.Status = "pending-trust"
		r.Detail = "Grok native ponytail installation requires explicit plugin trust; dot does not grant trust"
		return r
	}
	plugins, err := e.grokInventory(ctx, bin)
	if err != nil {
		r.Status = "unknown"
		r.Detail = "Grok native plugin inventory unavailable or unrecognized"
		return r
	}
	latest, metadataErr := e.latest(ctx, pluginSpecs()["ponytail"].metadata)
	r.Latest = latest
	target := latest
	if pin != "" {
		target = strings.TrimPrefix(pin, "v")
	}
	for _, p := range plugins {
		if p.Name != "ponytail" {
			continue
		}
		r.Installed = p.Version
		source := strings.TrimSuffix(p.Source, ".git")
		nativePin := false
		if stableVersion.MatchString(p.Version) && strings.HasSuffix(source, "@v"+p.Version) {
			nativePin = true
			source = strings.TrimSuffix(source, "@v"+p.Version)
		}

		if source != "DietrichGebert/ponytail" && source != "https://github.com/DietrichGebert/ponytail" {
			r.Status = "deferred-provenance"
			r.Detail = "native source/ref differs from the unpinned official source; preserved"
			return r
		}
		if p.Trusted == nil || p.Enabled == nil {
			r.Status = "partial"
			r.Detail = "native inventory confirms installation but omits trust/enablement state; review in Grok"
			return r
		}
		if !*p.Trusted {
			r.Status = "pending-trust"
			r.Detail = "native installation exists but plugin trust has not been granted"
			return r
		}
		if !*p.Enabled {
			r.Status = "pending-activation"
			r.Detail = "native plugin installed but disabled; enable through Grok's plugin manager"
			return r
		}
		if metadataErr != nil {
			r.Status = "unknown"
			r.Detail = "native plugin installed; stable metadata unavailable"
			return r
		}
		if !stableVersion.MatchString(p.Version) {
			r.Status = "unknown"
			r.Detail = "native installed version is not a verified stable version"
			return r
		}
		if nativePin {
			r.Status = "pinned"
			r.Detail = "official native release ref and trust verified; preserve native pin"
			return r
		}
		if p.Version == target {
			r.Status = "installed"
			if pin != "" {
				r.Status = "pinned"
			}
			r.Detail = "native source, version, trust and enablement verified"
			return r
		}
		if pin != "" {
			r.Status = "deferred-pin"
			r.Detail = "native update cannot guarantee the requested pinned ref; preserved"
			return r
		}
		if op == Inspect {
			r.Status = "update-available"
			return r
		}
		if e.opts.DryRun {
			r.Status = "planned"
			r.Detail = "stable update requires native exact-ref installation and user trust review"
			return r
		}
		if op == Ensure {
			r.Status = "installed"
			r.Detail = "update available; run dot ai update"
			return r
		}
		r.Status = "deferred-stable-ref"
		r.Detail = "native Grok update cannot pin a stable target; review: grok plugin install DietrichGebert/ponytail@v" + target + " (trust remains a native user decision)"
		return r

	}
	r.Status = "pending-trust"
	r.Detail = "run grok plugin install DietrichGebert/ponytail"
	if target != "" {
		r.Detail += "@v" + target
	}
	r.Detail += " and review native trust; dot never passes --trust"
	if e.opts.DryRun && op != Inspect {
		r.Status = "planned"
	}
	return r
}
