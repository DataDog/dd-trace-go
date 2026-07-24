package fixtureconflictingidentities

func readConfig() {
	_ = readEnv("DD_CONFLICTING_ALIAS")
	_, _ = wrappedEnv("DD_CONFLICTING_WRAPPER")
}
