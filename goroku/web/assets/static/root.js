/* R4.2: minimal RU/EN string map for the onboarding flow. No framework —
   applyI18n() walks [data-i18n] (textContent) and [data-i18n-html] (innerHTML)
   and the t() helper is used for strings emitted from JS. */
var STRINGS = {
    en: {
        skip_link: "Skip to onboarding",
        get_started: "Get started",
        add_phone: "Add phone",
        change_api: "Change API Creds",
        continue: "Continue",
        auth_phone: "Auth using phone instead",
        api_id_label: "Telegram API ID: ",
        api_hash_label: "Telegram API hash: ",
        phone_label: "Phone: ",
        custom_bot_label: "Inline bot username (E.g. @username_bot): ",
        custom_bot_placeholder: "Leave empty to generate automatically",
        waiting_auth: "Waiting for authentification...",
        confirm_auth: 'Please, confirm action in <span style="color:#28a0dc">Telegram</span>',
        code_caption: "Enter the code you recieved from Telegram",
        code_caption_tg: "Enter the code you recieved in Telegram",
        code_label: "Telegram code",
        enter_btn: "Enter",
        eula_text: 'You are <span style="color:#c54245">prohibited</span> from adding more than 1 account on the current platform by its EULA.',
        installed_title: "Goroku is installed",
        installed_desc: "Goroku is installed. You can close this page now.",
        installed_patience: "It might take a while for installation to fully complete. Please, be patient.",
        installed_restart: "Goroku will restart and might send several configuration messages to complete the installation!",
        installed_check_tg: 'Check <span style="color:#28a0dc">Telegram</span> for a message from your <b>inline bot</b>',
        best_userbot: 'Incomprehensibly <span style="color:#c54245">the best</span> userbot',
        authorized: "Authorized!",
        qr_guide_1: "Open Telegram on your phone",
        qr_guide_2: "Go to <b>Settings</b> &rarr; <b>Devices</b> &rarr; <b>Link Desktop Device</b>",
        qr_guide_3: "Point your phone at this screen to confirm login",
        two_fa_caption: "Enter your Telegram 2FA password, then press <span style='color: #dc137b;'>Enter</span>",
        setup_token_missing: "You are without a setup token. Open the web via a link with ?setup_token=...",
        code_timeout: "Code waiting timeout exceeded. Reload page and try again.",
        qr_failed: "QR login failed: ",
        finish_failed: "Finish login failed: ",
        save_creds_error: "Error occured while saving credentials: ",
        code_send_failed: "Code send failed: ",
        custom_bot_error: "Custom bot setting error: ",
        bot_username_invalid: "Bot username invalid",
        bot_username_invalid_text: "It must end with `bot` and be at least 5 symbols in length",
        bot_occupied: "This bot username is already occupied!",
        qr_link_fallback: "Open Telegram QR login link",
        error_title: "Error",
        auth_failed: "Auth failed: "
    },
    ru: {
        skip_link: "Перейти к настройке",
        get_started: "Начать",
        add_phone: "Добавить номер",
        change_api: "Сменить API-доступы",
        continue: "Продолжить",
        auth_phone: "Войти по номеру телефона",
        api_id_label: "Telegram API ID: ",
        api_hash_label: "Telegram API hash: ",
        phone_label: "Номер: ",
        custom_bot_label: "Юзернейм инлайн-бота (напр. @username_bot): ",
        custom_bot_placeholder: "Оставьте пустым для автогенерации",
        waiting_auth: "Ожидание авторизации...",
        confirm_auth: 'Подтвердите действие в <span style="color:#28a0dc">Telegram</span>',
        code_caption: "Введите код из Telegram",
        code_caption_tg: "Введите код из Telegram",
        code_label: "Код Telegram",
        enter_btn: "Ввод",
        eula_text: 'Вам <span style="color:#c54245">запрещено</span> добавлять более 1 аккаунта на текущей платформе согласно её EULA.',
        installed_title: "Goroku установлен",
        installed_desc: "Goroku установлен. Вы можете закрыть эту страницу.",
        installed_patience: "Установка может занять некоторое время. Пожалуйста, подождите.",
        installed_restart: "Goroku перезапустится и может отправить несколько конфигурационных сообщений для завершения установки!",
        installed_check_tg: 'Проверьте <span style="color:#28a0dc">Telegram</span> — придёт сообщение от вашего <b>инлайн-бота</b>',
        best_userbot: 'Невообразимо <span style="color:#c54245">лучший</span> юзербот',
        authorized: "Авторизовано!",
        qr_guide_1: "Откройте Telegram на телефоне",
        qr_guide_2: "Перейдите в <b>Настройки</b> &rarr; <b>Устройства</b> &rarr; <b>Подключить десктоп</b>",
        qr_guide_3: "Наведите телефон на этот экран для подтверждения входа",
        two_fa_caption: "Введите пароль 2FA от Telegram и нажмите <span style='color: #dc137b;'>Enter</span>",
        setup_token_missing: "Вы без setup-токена. Откройте веб по ссылке с ?setup_token=...",
        code_timeout: "Время ожидания кода истекло. Перезагрузите страницу и попробуйте снова.",
        qr_failed: "Ошибка QR-входа: ",
        finish_failed: "Ошибка завершения входа: ",
        save_creds_error: "Ошибка сохранения доступов: ",
        code_send_failed: "Ошибка отправки кода: ",
        custom_bot_error: "Ошибка установки бота: ",
        bot_username_invalid: "Неверный юзернейм бота",
        bot_username_invalid_text: "Должен заканчиваться на `bot` и быть не короче 5 символов",
        bot_occupied: "Этот юзернейм бота уже занят!",
        qr_link_fallback: "Открыть ссылку QR-входа Telegram",
        error_title: "Ошибка",
        auth_failed: "Ошибка авторизации: "
    }
};

var currentLang = (function () {
    try {
        var saved = localStorage.getItem("goroku_lang");
        if (saved === "ru" || saved === "en") {
            return saved;
        }
    } catch (e) {}
    return "en";
})();

function t(key) {
    var bundle = STRINGS[currentLang] || STRINGS.en;
    return Object.prototype.hasOwnProperty.call(bundle, key) ? bundle[key] : (STRINGS.en[key] || key);
}

function applyI18n(lang) {
    if (lang === "ru" || lang === "en") {
        currentLang = lang;
        try { localStorage.setItem("goroku_lang", lang); } catch (e) {}
    }
    document.documentElement.lang = currentLang;
    document.querySelectorAll("[data-i18n]").forEach(function (el) {
        el.textContent = t(el.getAttribute("data-i18n"));
    });
    document.querySelectorAll("[data-i18n-html]").forEach(function (el) {
        el.innerHTML = t(el.getAttribute("data-i18n-html"));
    });
    var customBot = document.getElementById("custom_bot");
    if (customBot) {
        customBot.setAttribute("placeholder", t("custom_bot_placeholder"));
    }
    document.querySelectorAll(".lang-btn").forEach(function (btn) {
        btn.setAttribute("aria-pressed", btn.getAttribute("data-lang") === currentLang ? "true" : "false");
    });
}

function getCookie(name) {
    const encoded = encodeURIComponent(name).replace(/[-.+*]/g, "\\$&");
    const match = document.cookie.match(new RegExp("(?:^|; )" + encoded + "=([^;]*)"));
    return match ? decodeURIComponent(match[1]) : "";
}

function csrfFetch(url, options = {}) {
    options = Object.assign({}, options);
    options.credentials = "include";
    const headers = new Headers(options.headers || {});
    const method = String(options.method || "GET").toUpperCase();
    if (method !== "GET" && method !== "HEAD" && method !== "OPTIONS") {
        const csrf = getCookie("csrf_token");
        if (csrf) {
            headers.set("X-CSRF-Token", csrf);
        }
    }
    options.headers = headers;
    return fetch(url, options);
}

function auth(c) {
    const previousMain = $(".main:visible");
    const restoreMain = () => (previousMain.length ? previousMain : $(".installation")).fadeIn(250);
    $(".main").fadeOut(250),
        setTimeout(() => {
            $(".auth")
                .hide()
                .fadeIn(250, () => {
                    $("#tg_icon").html(""),
                        bodymovin.loadAnimation({
                            container: document.getElementById("tg_icon"),
                            renderer: "canvas",
                            loop: !0,
                            autoplay: !0,
                            rendererSettings: {
                                clearCanvas: !0
                            },
                        });
                }),
                csrfFetch("/web_auth", {
                    method: "POST",
                    timeout: 25e4,
                })
                    .then((b) => b.text())
                    .then((a) => {
                        a = a.trim();
                        if ("SETUP_TOKEN_REQUIRED" == a) {
                            error_message(t("setup_token_missing"));
                            return void $(".auth").fadeOut(250, () => {
                                restoreMain();
                            });
                        }
                        if ("TIMEOUT" == a) {
                            error_message(t("code_timeout"));
                            return void $(".auth").fadeOut(250, () => {
                                restoreMain();
                            });
                        }
                        if (a.startsWith("goroku_")) {
                            (auth_required = !1),
                                $(".authorized").hide().fadeIn(100),
                                $(".auth").fadeOut(250, () => {
                                    $(".installation").fadeIn(250);
                                }),
                                c();
                        }
                    });
        }, 250);
}
var qr_interval = null,
    qr_login = !1,
    old_qr_sizes = [
        document.querySelector(".qr_inner").style.width,
        document.querySelector(".qr_inner").style.height,
    ];
(document.querySelector(".qr_inner").style.width = "100px"),
    (document.querySelector(".qr_inner").style.height = "100px");

function login_qr() {
    $("#continue_btn").fadeOut(100),
        $("#denyqr").hide().fadeIn(250),
        $(".title, .description").fadeOut(250),
        csrfFetch("/init_qr_login", {
            method: "POST",
            credentials: "include"
        })
            .then((b) => b.text().then((text) => ({ok: b.ok, text: text.trim()})))
            .then(({ok, text}) => {
                if (!ok) {
                    error_message(t("qr_failed") + text);
                    return void $("#denyqr").fadeOut(250, () => {
                        $("#continue_btn, .title, .description").hide().fadeIn(250);
                    });
                }
                if (typeof QRCodeStyling === "undefined") {
                    console.error("QRCodeStyling library is not loaded");
                    document.querySelector(".qr_inner").innerHTML =
                        '<a href="' + text + '" target="_blank" rel="noopener noreferrer">' + t("qr_link_fallback") + '</a>';
                    return;
                }
                const d = new QRCodeStyling({
                    width: window.innerHeight / 3,
                    height: window.innerHeight / 3,
                    type: "svg",
                    data: text,
                    dotsOptions: {
                        type: "rounded"
                    },
                    cornersSquareOptions: {
                        type: "extra-rounded"
                    },
                    backgroundOptions: {
                        color: "transparent"
                    },
                    imageOptions: {
                        imageSize: 0.4,
                        margin: 8
                    },
                    qrOptions: {
                        errorCorrectionLevel: "M"
                    },
                });
                (document.querySelector(".qr_inner").innerHTML = ""),
                    (document.querySelector(".qr_inner").style.width = old_qr_sizes[0]),
                    (document.querySelector(".qr_inner").style.height = old_qr_sizes[1]),
                    d.append(document.querySelector(".qr_inner")),
                    (qr_interval = setInterval(() => {
                        csrfFetch("/get_qr_url", {
                            method: "POST",
                            credentials: "include"
                        })
                            .then((b) => b.text())
                            .then((b) =>
                                "SUCCESS" == b || "2FA" == b ?
                                    ($("#block_qr_login").fadeOut(250),
                                        $("#denyqr").fadeOut(250),
                                        $("#continue_btn, .title, .description").hide().fadeIn(250),
                                        "SUCCESS" == b && switch_block("custom_bot"),
                                        "2FA" == b && (show_2fa(), (qr_login = !0)),
                                        void clearInterval(qr_interval)) :
                                    void d.update({
                                        data: b
                                    }),
                            );
                    }, 1250));
            });
}
$("#get_started").click(() => {
    fetch("/can_add", {
        method: "POST",
        credentials: "include"
    }).then((b) =>
        b.ok ?
            auth_required ?
                auth(() => {
                    $("#get_started").click();
                }) :
                void ($("#continue_btn").hide().fadeIn(250),
                    $("#denyqr").hide(),
                    $("#enter_api").fadeOut(250),
                    $("#get_started").fadeOut(250, () => {
                        switch_block(_current_block);
                    })) :
            void show_eula(),
    );
}),
    $("#enter_api").click(() =>
        auth_required ?
            auth(() => {
                $("#enter_api").click();
            }) :
            void ($("#get_started").fadeOut(250),
                $("#enter_api").fadeOut(250, () => {
                    $("#continue_btn").hide().fadeIn(250), switch_block("api_id");
                })),
    );

function isInt(c) {
    var a = parseFloat(c);
    return !isNaN(c) && (0 | a) === a;
}

function isValidPhone(b) {
    return /^[+]?\d{11,13}$/.test(b);
}

function finish_login() {
    console.log("Done")
    csrfFetch("/finish_login", {
        method: "POST",
        credentials: "include"
    }).then((b) => {
        if (!b.ok) {
            b.text().then((text) => error_message(t("finish_failed") + text));
            return;
        }
        $(".finish_block").fadeIn()
        $("#installation_icon").html("")
        $(".installation").fadeOut()
        bodymovin.loadAnimation({
            container: document.getElementById("installation_icon"),
            renderer: "canvas",
            loop: !0,
            autoplay: !0,
            rendererSettings: {
                clearCanvas: !0
            },
        })
    }).catch((b) => {
        error_message(t("finish_failed") + b.toString());
    })
}

function show_2fa() {
    $(".auth-code-form")
        .hide()
        .fadeIn(250, () => {
            $("#monkey-close").html(""),
                (anim = bodymovin.loadAnimation({
                    container: document.getElementById("monkey-close"),
                    renderer: "canvas",
                    loop: !0,
                    autoplay: !0,
                    rendererSettings: {
                        clearCanvas: !0
                    },
                })),
                anim.addEventListener("complete", () => {
                    setTimeout(() => {
                        anim.goToAndPlay(0);
                    }, 2e3);
                });
        }),
        $(".code-input").removeAttr("disabled"),
        $(".code-input").attr("inputmode", "text"),
        $(".code-input").attr("autocomplete", "off"),
        $(".code-input").attr("autocorrect", "off"),
        $(".code-input").attr("autocapitalize", "off"),
        $(".code-input").attr("spellcheck", "false"),
        $(".code-input").attr("type", "password"),
        $(".enter").hasClass("tgcode") && $(".enter").removeClass("tgcode"),
        $(".code-caption").html(t("two_fa_caption")),
        cnt_btn.setAttribute("current-step", "2fa"),
        $("#monkey").hide(),
        $("#monkey-close").hide().fadeIn(100),
        (_current_block = "2fa");
}

function show_eula() {
    $(".main").fadeOut(250),
        $(".eula-form")
            .hide()
            .fadeIn(250, () => {
                $("#law").html(""),
                    (anim = bodymovin.loadAnimation({
                        container: document.getElementById("law"),
                        renderer: "canvas",
                        loop: !0,
                        autoplay: !0,
                        rendererSettings: {
                            clearCanvas: !0
                        },
                    }));
            });
}

function tg_code(b = false) {
    return b && qr_login ?
        void csrfFetch("/qr_2fa", {
            method: "POST",
            credentials: "include",
            body: _2fa_pass,
        }).then((b) => {
            b.ok ?
                (console.log("ko"),
                finish_login(),
                    $("#block_phone").fadeOut(),
                    $(".auth-code-form").fadeOut()) :
                ($(".code-input").removeAttr("disabled"),
                    b.text().then((b) => {
                        error_state(), Swal.fire(t("error_title"), b, "error");
                    }));
        })
        :
        void csrfFetch("/tg_code", {
            method: "POST",
            body: `${_tg_pass}\n${_phone}\n${_2fa_pass}`,
        })
            .then((b) => {
                b.ok ?
                    (console.log("ok"),
                    finish_login(),
                        $("#block_phone").fadeOut(),
                        $(".auth-code-form").fadeOut()) :
                    401 == b.status ?
                        show_2fa() :
                        ($(".code-input").removeAttr("disabled"),
                            b.text().then((b) => {
                                error_state(), Swal.fire(t("error_title"), b, "error");
                            }));

            })
            .catch((b) => {
                Swal.showValidationMessage(t("auth_failed") + b.toString());
            });
}

function switch_block(b) {
    cnt_btn.setAttribute("current-step", b);
    try {
        $(`#block_${_current_block}`).fadeOut(() => {
            $(`#block_${b}`).hide().fadeIn();
        });
    } catch {
        $(`#block_${b}`).hide().fadeIn();
    }
    (_current_block = b), "qr_login" == _current_block && login_qr();
}

function error_message(b) {
    Swal.fire({
        icon: "error",
        title: b
    });
}

function error_state() {
    $("body").addClass("red_state"),
        (cnt_btn.disabled = !0),
        setTimeout(() => {
            (cnt_btn.disabled = !1), $("body").removeClass("red_state");
        }, 1e3);
}
var _api_id = "",
    _api_hash = "",
    _phone = "",
    _2fa_pass = "",
    _tg_pass = "",
    _current_block = skip_creds ? "qr_login" : "api_id";

function is_phone() {
    return /Android|webOS|iPhone|iPad|iPod|BlackBerry|BB|PlayBook|IEMobile|Windows Phone|Kindle|Silk|Opera Mini/i.test(
        navigator.userAgent,
    );
}
const cnt_btn = document.querySelector("#continue_btn");

function process_next() {
    let b = cnt_btn.getAttribute("current-step");
    if ("api_id" == b) {
        let b = document.querySelector("#api_id").value;
        return 4 > b.length || !isInt(b) ?
            void error_state() :
            ((_api_id = parseInt(b, 10)), void switch_block("api_hash"));
    }
    if ("api_hash" == b) {
        let b = document.querySelector("#api_hash").value;
        return 32 == b.length ?
            ((_api_hash = b),
                void csrfFetch("/set_api", {
                    method: "PUT",
                    body: _api_hash + _api_id,
                    credentials: "include",
                })
                    .then((b) => b.text())
                    .then((b) => {
                        "ok" == b
                            ?
                            switch_block("qr_login") :
                            (error_state(), error_message(b));
                    })
                    .catch((b) => {
                        error_state(),
                            error_message(
                                t("save_creds_error") + b.toString(),
                            );
                    })) :
            void error_state();
    }
    if ("phone" == b) {
        let b = document.querySelector("#phone").value;
        if (!isValidPhone(b)) return void error_state();
        (_phone = b),
            csrfFetch("/send_tg_code", {
                method: "POST",
                body: _phone,
                credentials: "include",
            })
                .then((b) => {
                    b.ok ?
                        ($(".auth-code-form")
                            .hide()
                            .fadeIn(250, () => {
                                $("#monkey").html(""),
                                    (anim2 = bodymovin.loadAnimation({
                                        container: document.getElementById("monkey"),
                                        renderer: "canvas",
                                        loop: !1,
                                        autoplay: !0,
                                        rendererSettings: {
                                            clearCanvas: !0
                                        },
                                    })),
                                    anim2.addEventListener("complete", () => {
                                        setTimeout(() => {
                                            anim2.goToAndPlay(0);
                                        }, 2e3);
                                    });
                            }),
                            $(".code-input").removeAttr("disabled"),
                            $(".enter").addClass("tgcode"),
                            $(".code-caption").text(
                                t("code_caption_tg"),
                            ),
                            $(".code-input").attr("autocomplete", "off"),
                            $(".code-input").attr("autocorrect", "off"),
                            $(".code-input").attr("autocapitalize", "off"),
                            $(".code-input").attr("spellcheck", "false"),
                            $(".code-input").attr("type", "number"),
                            cnt_btn.setAttribute("current-step", "code"),
                            (_current_block = "code")) :
                        403 == b.status ?
                            show_eula() :
                            b.text().then((b) => {
                                error_state(), error_message(b);
                            });
                })
                .catch((b) => {
                    error_state(), error_message(t("code_send_failed") + b.toString());
                });
    }
    if ("2fa" == b) {
        let b = document.querySelector("#_2fa").value;
        return (_2fa_pass = b), void tg_code();
    }
    if ("custom_bot" == b) {
        let b = document.querySelector("#custom_bot").value;
        return "" != b && (!b.toLowerCase().endsWith("bot") || 5 > b.length) ?
            void Swal.fire({
                icon: "error",
                title: t("bot_username_invalid"),
                text: t("bot_username_invalid_text"),
            }) :
            "" == b ?
                void finish_login() :
                void csrfFetch("/custom_bot", {
                    method: "POST",
                    credentials: "include",
                    body: b,
                })
                    .then((b) => b.text())
                    .then((b) =>
                        "OCCUPIED" == b ?
                            void Swal.fire({
                                icon: "error",
                                title: t("bot_occupied"),
                            }) :
                            void finish_login(),
                    )
                    .catch((b) => {
                        error_state(),
                            error_message(t("custom_bot_error") + b.toString());
                    });
    }
}
(cnt_btn.onclick = () =>
    cnt_btn.disabled ?
        void 0 :
        auth_required ?
            auth(() => {
                cnt_btn.click();
            }) :
            void process_next()),
    $("#denyqr").on("click", () => {
        qr_interval && clearInterval(qr_interval),
            $("#denyqr").fadeOut(250),
            $("#continue_btn, .title, .description").hide().fadeIn(250),
            switch_block("phone");
    }),
    $(".installation input").on("keyup", (b) =>
        cnt_btn.disabled ?
            void 0 :
            auth_required ?
                auth(() => {
                    cnt_btn.click();
                }) :
                void (("Enter" === b.key || 13 === b.keyCode) && process_next()),
    ),
    $(".code-input").on("keyup", (b) => {
        if ("code" == _current_block && 5 == $(".code-input").val().length)
            (_tg_pass = $(".code-input").val()),
                $(".code-input").attr("disabled", "true"),
                $(".code-input").val(""),
                tg_code();
        else if (
            "2fa" == _current_block &&
            ("Enter" === b.key || 13 === b.keyCode)
        ) {
            let b = $(".code-input").val();
            (_2fa_pass = b),
                $(".code-input").attr("disabled", "true"),
                $(".code-input").val(""),
                tg_code(true);
            console.log("2fa True")
        }
    }),
    $(".enter").on("click", () => {
        if ("2fa" == _current_block) {
            let b = $(".code-input").val();
            (_2fa_pass = b),
                $(".code-input").attr("disabled", "true"),
                $(".code-input").val(""),
                tg_code(true);
        }
    })

$(".lang-btn").on("click", function () {
    applyI18n(this.getAttribute("data-lang"));
});

applyI18n(currentLang);
