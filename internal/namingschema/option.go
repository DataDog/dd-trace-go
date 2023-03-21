package namingschema

type VersionOverrideFunc func() string

type Option func(cfg *config)

type config struct {
	versionOverrides map[Version]VersionOverrideFunc
}

func WithVersionOverride(v Version, f VersionOverrideFunc) Option {
	return func(cfg *config) {
		if cfg.versionOverrides == nil {
			cfg.versionOverrides = make(map[Version]VersionOverrideFunc)
		}
		cfg.versionOverrides[v] = f
	}
}
