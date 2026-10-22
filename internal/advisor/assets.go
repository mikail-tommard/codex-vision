package advisor

import _ "embed"

//go:embed web/index.html
var panelHTML string

//go:embed web/style.css
var panelCSS string

//go:embed web/app.js
var panelJS string
