package input

import "testing"

func TestLaunchValidation(t *testing.T) {
	process := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	for _, url := range []string{"", "https://www.youtube.com/", "http://localhost:3000/path?q=a%20b"} {
		if err := ValidateLaunch(process, url); err != nil {
			t.Fatal(url, err)
		}
	}
	for _, process := range []string{"", "Google Chrome", "/bin/sh", "/Applications/Test.app/../Test.app/Contents/MacOS/Test", "/Applications/Test.app/Contents/Helpers/helper", "/Applications/Test.app/Contents/MacOS/Test\x00", "/Applications/Test.app/Contents/MacOS/Test\n"} {
		if ValidateLaunch(process, "") == nil {
			t.Fatalf("accepted executable %q", process)
		}
	}
	for _, url := range []string{"file:///etc/passwd", "javascript:alert(1)", "chrome://settings", "https://", "https://user:password@example.com/", "https://example.com/\n", "https://example.com/%zz", "--args", "//example.com/"} {
		if ValidateLaunch(process, url) == nil {
			t.Fatalf("accepted URL %q", url)
		}
	}
}
