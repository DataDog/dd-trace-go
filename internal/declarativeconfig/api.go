package declarativeconfig

func GetString(key string) (string, bool) {
	return config.getString(key)
}

func GetBool(key string) (bool, bool) {
	return config.getBool(key)
}
