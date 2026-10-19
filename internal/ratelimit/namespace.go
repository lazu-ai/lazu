package ratelimit

func requestLimitKey(namespace, key string) string {
	if namespace == "" || key == "" {
		return key
	}
	return namespace + ":" + key
}
