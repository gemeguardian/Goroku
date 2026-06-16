package inline

// DetectCallingModuleForTest exposes detectCallingModule for testing from other packages.
func DetectCallingModuleForTest(im *InlineManager) string {
	return im.detectCallingModule()
}
