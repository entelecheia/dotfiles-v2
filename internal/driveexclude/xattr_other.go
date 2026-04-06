//go:build !darwin

package driveexclude

func setIgnoreContent(path string) error {
	return nil
}

func hasIgnoreContent(path string) (bool, error) {
	return false, nil
}

func removeIgnoreContent(path string) error {
	return nil
}
