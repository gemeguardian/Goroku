package web

import "embed"

// embeddedAssets bundles the offline web onboarding resources (templates and
// static JS/CSS/images) into the binary so the panel works with zero external
// network access in the browser. Disk resources under
// $GOROKU_WEB_RESOURCES / <dataRoot>/web-resources still take precedence when
// present, so operators can override; the embedded tree is the offline default
// and fallback used by RootHandler and the /static/ handler.
//
// R4.1: all vendored libraries (jQuery, SweetAlert2, qr-code-styling) live under
// assets/static/; the external lottie/bodymovin CDN dependency was replaced by
// the local static/lottie-shim.js.
//
//go:embed assets
var embeddedAssets embed.FS
