package main

import "regexp"

var (
	scriptTagPattern = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	styleTagPattern  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	eventAttrPattern = regexp.MustCompile(`(?i)\s+on[a-z]+\s*=\s*"[^"]*"`)
	jsHrefPattern    = regexp.MustCompile(`(?i)href\s*=\s*"\s*javascript:[^"]*"`)
)

func sanitizePageHTML(input string) string {
	output := scriptTagPattern.ReplaceAllString(input, "")
	output = styleTagPattern.ReplaceAllString(output, "")
	output = eventAttrPattern.ReplaceAllString(output, "")
	output = jsHrefPattern.ReplaceAllString(output, `href="#"`)
	return output
}
