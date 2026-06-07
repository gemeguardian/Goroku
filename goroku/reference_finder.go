package goroku

// ReplaceAllRefs is a stub in Go for Python's replace_all_refs.
// In Python, this is used to hot-swap modules in memory using gc.get_referrers.
// Since Go is a statically compiled language with a static registry, memory hot-swapping is bypassed.
func ReplaceAllRefs(replaceFrom, replaceTo interface{}) interface{} {
	return replaceFrom
}
