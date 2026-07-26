/*
 * lottie-shim.js — R4.1 offline replacement for bodymovin/lottie.
 *
 * The original onboarding loaded the bodymovin library from a CDN and streamed
 * animation JSON from remote animation hosts. Those are all external origins,
 * which breaks the "zero external network access" goal and is flagged by the
 * residual roadmap (R4.1). The animations are purely decorative (loading
 * spinners around the auth/QR/installation steps), so this tiny shim exposes
 * the same `bodymovin.loadAnimation(opts)` surface the page already calls and
 * renders a local CSS spinner into the requested container. It intentionally
 * ignores any remote `path` and stubs the playback control surface
 * (`addEventListener`, `goToAndPlay`, ...) so existing root.js stays unchanged.
 */
(function () {
    "use strict";

    function makeAnim() {
        var listeners = { complete: [] };
        setTimeout(function () {
            (listeners.complete || []).forEach(function (cb) {
                try { cb(); } catch (e) {}
            });
        }, 1500);
        return {
            addEventListener: function (evt, cb) {
                if (listeners[evt]) {
                    listeners[evt].push(cb);
                }
            },
            removeEventListener: function () {},
            goToAndPlay: function () {},
            goToAndStop: function () {},
            play: function () {},
            stop: function () {},
            setSpeed: function () {},
            setDirection: function () {},
            destroy: function () {}
        };
    }

    function loadAnimation(opts) {
        var container = opts && opts.container;
        if (container) {
            container.innerHTML = "";
            var node = document.createElement("div");
            node.className = "lottie-spinner";
            node.setAttribute("aria-hidden", "true");
            container.appendChild(node);
        }
        return makeAnim();
    }

    var bodymovin = { loadAnimation: loadAnimation };

    if (typeof window !== "undefined") {
        window.bodymovin = bodymovin;
    }
    if (typeof module !== "undefined" && module.exports) {
        module.exports = bodymovin;
    }
})();
