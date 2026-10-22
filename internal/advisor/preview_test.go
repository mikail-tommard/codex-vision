package advisor

import "os"

func previewEnabled() bool { return os.Getenv("CODEX_VISION_ADVISOR_PREVIEW") == "1" }
