/* NotifyRelay operator UI.
 *
 * Two jobs: send state-changing requests with the header the server requires,
 * and keep the generated form's conditional fields in step with the values
 * above them. Everything else is server-rendered.
 */
(function () {
  "use strict";

  var CSRF_HEADER = "X-NotifyRelay-Admin";

  /* -------------------------------------------------------------- strings */

  /* The copy the script shows, rendered into the page by the server.
   *
   * It is in the page rather than fetched, because the script needs it before
   * it can report a failure, and a request that can fail is a second failure
   * mode on the path that exists to report the first one.
   *
   * A missing key falls back to the key's own name. That is wrong, and
   * deliberately visible: a blank message reads as "the button did nothing",
   * which is the hardest kind of bug to describe in a report. */
  var T = (function () {
    var node = document.getElementById("i18n");
    if (!node) { return {}; }
    try { return JSON.parse(node.textContent) || {}; } catch (e) { return {}; }
  })();

  function t(key) {
    var value = T[key];
    if (typeof value !== "string" || value === "") { return key; }
    return value;
  }

  /* fill substitutes the table's placeholders in order.
   *
   * Only %s and %d, which is the whole set the table is allowed to use — a
   * translator has to be able to reorder the sentence, and that only works if
   * the values are positional rather than baked into a concatenation. */
  function fill(template, values) {
    var i = 0;
    return template.replace(/%[sd]/g, function () {
      return i < values.length ? String(values[i++]) : "";
    });
  }

  /* testMessage renders a connectivity check's outcome.
   *
   * The detail comes from the channel and is English: it names what the channel
   * did, and translating it would mean a copy table per channel package. It is
   * shown after the outcome rather than instead of it, so the sentence the
   * operator reads first is in their own language. */
  function testMessage(res) {
    if (res.ok) { return fill(t("TestOK"), [res.detail || ""]); }
    return fill(t("TestFailed"), [res.class || "", res.error || res.detail || ""]);
  }

  /* ---------------------------------------------------------------- flash */

  /* A flash message rides in the query string so a redirect after a form post
   * can say what happened. The server has already rendered it into the page by
   * the time this runs, so the parameter is dropped from the address bar here.
   *
   * Leaving it there replays the message on every reload — "Saved webhook" for
   * a save from ten minutes ago — and a link copied out of the address bar
   * carries somebody else's message to whoever opens it. */
  (function () {
    var query = window.location.search;
    if (query.indexOf("ok=") < 0 && query.indexOf("err=") < 0) { return; }
    try {
      var params = new URLSearchParams(query);
      params.delete("ok");
      params.delete("err");
      var rest = params.toString();
      window.history.replaceState(null, "",
        window.location.pathname + (rest ? "?" + rest : "") + window.location.hash);
    } catch (e) {
      /* No URLSearchParams or no history API: the message stays in the URL,
       * which is untidy and not worth breaking the page over. */
    }
  })();

  /* ------------------------------------------------------------- requests */

  function request(method, path, body) {
    var init = {
      method: method,
      headers: {},
      credentials: "same-origin"
    };
    init.headers[CSRF_HEADER] = "1";
    if (body !== undefined) {
      init.headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(body);
    }

    return fetch(path, init).then(function (res) {
      return res.text().then(function (text) {
        var parsed = null;
        try { parsed = text ? JSON.parse(text) : null; } catch (e) { /* not JSON */ }
        if (!res.ok) {
          var message = (parsed && (parsed.message || parsed.error)) || text || res.statusText;
          var err = new Error(message);
          /* The code and the status travel with the error so a caller can
           * answer a specific refusal specifically. Without them the only
           * option is to match on the message, which is a string that exists
           * to be read by a person and will be reworded. */
          err.status = res.status;
          err.code = parsed && parsed.error;
          /* The per-field complaints, where the server had them. A refusal that
           * names parameters can be shown under the boxes they are about
           * instead of as one sentence at the bottom. */
          err.fields = parsed && parsed.fields;
          throw err;
        }
        return parsed;
      });
    });
  }

  function say(el, message, ok) {
    if (!el) { return; }
    el.textContent = message;
    el.className = "flash " + (ok ? "ok" : "bad");
    el.hidden = false;
  }

  function escapeSelector(value) {
    /* CSS.escape is not in every browser this might run in, and the values
     * here are parameter names from our own schema. */
    if (window.CSS && window.CSS.escape) { return window.CSS.escape(value); }
    return String(value).replace(/[^a-zA-Z0-9_-]/g, "\\$&");
  }

  /* ---------------------------------------------------------------- login */

  var loginForm = document.getElementById("login-form");
  if (loginForm) {
    loginForm.addEventListener("submit", function (event) {
      event.preventDefault();
      var error = document.getElementById("login-error");
      request("POST", "/admin/api/login", {
        username: document.getElementById("username").value,
        password: document.getElementById("password").value
      }).then(function () {
        window.location.href = "/admin/channels";
      }).catch(function (err) {
        say(error, err.message, false);
        document.getElementById("password").value = "";
      });
    });
  }

  /* -------------------------------------------------------------- sign out */

  var signOut = document.getElementById("sign-out");
  if (signOut) {
    signOut.addEventListener("click", function () {
      request("POST", "/admin/api/logout").then(function () {
        window.location.href = "/admin/login";
      }).catch(function () {
        window.location.href = "/admin/login";
      });
    });
  }

  /* --------------------------------------------------- generated form state */

  function valueOf(scope, name) {
    var el = scope.querySelector('[name="' + escapeSelector(name) + '"]');
    if (!el) { return null; }
    if (el.type === "checkbox") { return el.checked ? "true" : "false"; }
    return el.value;
  }

  /* applyConditions shows or hides each conditional field and moves its
   * required marker with it.
   *
   * Hiding a required field without clearing its marker is how a form tells
   * somebody to fill in a box it is not showing — and the reverse, showing a
   * box without saying it is required, is how they find out at save time. */
  function applyConditions(scope) {
    Array.prototype.forEach.call(scope.querySelectorAll(".field"), function (field) {
      var dependsOn = field.getAttribute("data-show-if-field");
      if (!dependsOn) { return; }

      var want = field.getAttribute("data-show-if-equals");
      var applies = valueOf(scope, dependsOn) === want;

      field.hidden = !applies;

      /* data-required is on the field wrapper, not on the control: the wrapper
       * is what carries the schema, and the control is generated inside it. The
       * lookup used to be on the control, so it never matched and no generated
       * input was ever required — the red asterisk was the whole mechanism. */
      var required = field.getAttribute("data-required") === "1";

      var marker = field.querySelector(".req");
      if (marker) { marker.hidden = !(applies && required); }

      var input = field.querySelector("input, select");
      if (input) { input.required = applies && required; }
    });
  }

  /* --------------------------------------------------------- channel form */

  var channelForm = document.getElementById("channel-form");
  if (channelForm) {
    var typeSelect = document.getElementById("channel-type");
    var result = document.getElementById("channel-result");

    function activeScope() {
      return channelForm.querySelector('.type-fields[data-type="' + escapeSelector(typeSelect.value) + '"]');
    }

    function showActiveType() {
      Array.prototype.forEach.call(channelForm.querySelectorAll(".type-fields"), function (group) {
        group.hidden = group.getAttribute("data-type") !== typeSelect.value;
      });
      var scope = activeScope();
      if (scope) { applyConditions(scope); }
    }

    typeSelect.addEventListener("change", showActiveType);
    channelForm.addEventListener("input", function (event) {
      var scope = activeScope();
      if (scope && scope.contains(event.target)) { applyConditions(scope); }
    });
    channelForm.addEventListener("change", function (event) {
      var scope = activeScope();
      if (scope && scope.contains(event.target)) { applyConditions(scope); }
    });
    showActiveType();

    /* collect reads the visible type's fields into the request body.
     *
     * A private field that is left blank is omitted rather than sent empty:
     * absent means "keep what is stored" and an empty string means "clear it".
     * The two have to be distinguishable, or an edit that changes the host
     * would wipe the password. */
    function collect() {
      var scope = activeScope();
      var config = {};
      if (!scope) { return null; }

      Array.prototype.forEach.call(scope.querySelectorAll(".field"), function (field) {
        if (field.hidden) { return; }

        var input = field.querySelector("input, select");
        if (!input) { return; }

        var name = input.getAttribute("name");
        if (!name) { return; }

        if (input.type === "checkbox") {
          config[name] = input.checked;
          return;
        }

        var clear = field.querySelector('[data-clear="' + escapeSelector(name) + '"]');
        if (clear) {
          if (clear.checked) {
            config[name] = "";
          } else if (input.value !== "") {
            config[name] = input.value;
          }
          return;
        }

        if (input.value === "") { return; }
        config[name] = input.value;
      });

      /* List parameters arrive as comma-separated text and have to become
       * arrays. The channel readers accept a bare string as a one-element
       * list, so "a, b" would otherwise be a single address containing a
       * comma — accepted, and wrong. */
      var descriptor = catalogFor(typeSelect.value);
      if (descriptor) {
        descriptor.parameters.forEach(function (spec) {
          if (spec.type !== "string_list") { return; }
          var value = config[spec.name];
          if (typeof value !== "string") { return; }
          config[spec.name] = value.split(",").map(function (s) { return s.trim(); })
            .filter(function (s) { return s !== ""; });
        });
      }

      var quota = {};
      Array.prototype.forEach.call(
        channelForm.querySelectorAll('[id^="q-"]'),
        function (input) {
          quota[input.getAttribute("name")] = parseInt(input.value, 10) || 0;
        }
      );

      return {
        name: document.getElementById("channel-name").value,
        type: typeSelect.value,
        enabled: document.getElementById("channel-enabled").checked,
        config: config,
        quota: quota,
        /* The name the form was opened for. The server needs it to tell "create
         * a channel called X" apart from "change the channel called X": both
         * arrive as the same body, and without this the first one silently
         * becomes the second whenever X already exists. */
        editing: channelForm.getAttribute("data-editing") || ""
      };
    }

    function saved(res) {
      if (res && res.live === false) {
        say(result, fill(t("SavedButNotLive"), [res.reload]), false);
        return;
      }
      window.location.href = "/admin/channels?ok=" +
        encodeURIComponent(fill(t("SavedFlash"), [document.getElementById("channel-name").value]));
    }

    /* placeFieldErrors moves a refusal's per-field complaints under the boxes
     * they are about, and returns the ones it could not place.
     *
     * The message the server sends is written to be read in a log line: it names
     * each parameter by its schema name and joins every problem into one
     * sentence. The operator is looking at a box labelled "SMTP server", and
     * that is where the complaint belongs.
     *
     * A complaint about a field that is not on screen is returned rather than
     * shown. The schema renders every channel type's block into the page and
     * hides the ones that do not apply, so a message under a hidden box is a
     * message pointing at nothing. */
    function placeFieldErrors(err) {
      var scope = activeScope();
      if (scope) {
        Array.prototype.forEach.call(scope.querySelectorAll(".field-error"), function (note) {
          note.remove();
        });
      }

      var fields = (err && err.fields) || {};
      var names = Object.keys(fields);
      var unplaced = [];
      if (!scope) { return names.map(function (n) { return fields[n]; }); }

      var first = null;
      names.forEach(function (name) {
        var field = scope.querySelector('.field[data-field="' + escapeSelector(name) + '"]');
        if (!field || field.hidden) {
          unplaced.push(fields[name]);
          return;
        }

        var note = document.createElement("p");
        note.className = "field-error";
        note.textContent = fields[name];
        field.appendChild(note);

        if (!first) { first = field.querySelector("input, select"); }
      });

      if (first) {
        first.focus();
        first.scrollIntoView({ block: "center" });
      }
      return unplaced;
    }

    function failed(err, body) {
      /* A refusal that named fields is shown under them. What could not be
       * placed — an unknown key, or a pair that is only wrong together — is
       * listed at the bottom, because the reason the save was refused has to
       * appear somewhere. */
      var unplaced = placeFieldErrors(err);
      if (unplaced.length > 0) {
        say(result, unplaced.join("\n"), false);
        return;
      }
      if (err.fields && Object.keys(err.fields).length > 0) {
        say(result, t("FixMarkedFields"), false);
        return;
      }

      /* A name collision is a question, not a failure. Saving would replace a
       * channel that is delivering right now, and the operator asked to create
       * a new one — so they are told what the name collides with, and asked
       * again with the answer attached. */
      if (err.code === "name_taken" && window.confirm(err.message + "\n\nReplace it?")) {
        body.replace = true;
        request("POST", "/admin/api/channels", body).then(saved).catch(function (again) {
          say(result, again.message, false);
        });
        return;
      }
      say(result, err.message, false);
    }

    channelForm.addEventListener("submit", function (event) {
      event.preventDefault();

      var body = collect();
      if (!body) {
        /* Reachable only if the type picker's required attribute is bypassed.
         * Saying so beats the TypeError that would otherwise surface as a
         * button that does nothing. */
        say(result, t("ChooseTypeFirst"), false);
        return;
      }

      request("POST", "/admin/api/channels", body)
        .then(saved)
        .catch(function (err) { failed(err, body); });
    });

    var testButton = document.getElementById("test-form");
    if (testButton) {
      testButton.addEventListener("click", function () {
        var name = document.getElementById("channel-name").value;
        if (!name) {
          say(result, t("SaveBeforeTest"), false);
          return;
        }
        request("POST", "/admin/api/channels/" + encodeURIComponent(name) + "/test")
          .then(function (res) { say(result, testMessage(res), res.ok); })
          .catch(function (err) { say(result, err.message, false); });
      });
    }
  }

  /* --------------------------------------------- sending a test notification */

  /* The one thing on the channel page that proves a notification arrives.
   *
   * It goes through the ordinary delivery path and comes back with the id of the
   * delivery it created, and that page is where the operator is sent — the
   * answer to "did it work" is the attempt history, not this dialog. A success
   * message here would be the interface claiming a delivery it has not seen yet.
   *
   * The channel is on the form's own data attribute rather than in a hidden
   * field: it is read from the page the server rendered, so it cannot be edited
   * into a different channel by whoever is posting. */
  var testNotifyForm = document.getElementById("test-notify-form");
  if (testNotifyForm) {
    var testNotifyResult = document.getElementById("test-notify-result");

    testNotifyForm.addEventListener("submit", function (event) {
      event.preventDefault();

      var name = testNotifyForm.getAttribute("data-channel");
      if (!name) { return; }

      request("POST", "/admin/api/channels/" + encodeURIComponent(name) + "/test-notification", {
        title: document.getElementById("test-notify-title").value,
        body: document.getElementById("test-notify-body").value
      }).then(function (res) {
        if (res && res.delivery_id) {
          window.location.href = "/admin/deliveries/" + encodeURIComponent(res.delivery_id);
          return;
        }
        window.location.href = "/admin/deliveries?ok=" + encodeURIComponent(t("TestNotifyQueued"));
      }).catch(function (err) { say(testNotifyResult, err.message, false); });
    });
  }

  /* The dialog is opened from a row's button, which carries the channel name.
   * The heading names it too, so there is no doubt which channel is about to
   * receive a real message. */
  document.addEventListener("click", function (event) {
    var target = event.target;
    if (!target.dataset || !target.dataset.testNotify) { return; }

    var name = target.dataset.testNotify;
    testNotifyForm.setAttribute("data-channel", name);

    var named = document.getElementById("test-notify-channel");
    if (named) { named.textContent = name; }

    var result = document.getElementById("test-notify-result");
    if (result) { result.hidden = true; }

    var dialog = document.getElementById("test-notify-dialog");
    if (dialog) { dialog.showModal(); }
  });

  var cancelTestNotify = document.getElementById("cancel-test-notify");
  if (cancelTestNotify) {
    cancelTestNotify.addEventListener("click", function () {
      var dialog = document.getElementById("test-notify-dialog");
      if (dialog) { dialog.close(); }
    });
  }

  /* ------------------------------------------------------ change password */

  var passwordForm = document.getElementById("password-form");
  if (passwordForm) {
    var passwordResult = document.getElementById("password-result");

    passwordForm.addEventListener("submit", function (event) {
      event.preventDefault();

      var next = document.getElementById("new-password").value;
      if (next !== document.getElementById("confirm-password").value) {
        say(passwordResult, t("PasswordsNoMatch"), false);
        return;
      }

      request("POST", "/admin/api/password", {
        current_password: document.getElementById("current-password").value,
        new_password: next
      }).then(function (res) {
        /* The count of ended sessions comes from the server rather than being
         * assumed. "Did that actually lock the other person out" is the question
         * the operator is asking, and the server is the only one that knows. */
        say(passwordResult, (res && res.message) || t("PasswordSet"), true);

        document.getElementById("current-password").value = "";
        document.getElementById("new-password").value = "";
        document.getElementById("confirm-password").value = "";
      }).catch(function (err) { say(passwordResult, err.message, false); });
    });
  }

  /* --------------------------------------------------- unsaved form changes */

  /* A form that has been edited and not saved asks before it is abandoned.
   *
   * The channel form holds a configuration that exists nowhere until Save, and
   * the Cancel link sits right next to Save. Clicking it is a navigation, which
   * is how a half-finished edit is lost without anybody deciding to lose it —
   * and the edit can be twenty fields of a channel type the operator just read
   * the documentation for.
   *
   * beforeunload covers the other exits: a reload, a bookmark, the back button.
   * The browser shows its own wording there and will not let this one supply
   * any, which is why the Cancel link gets a confirm() of ours instead. */
  (function () {
    var form = document.getElementById("channel-form");
    if (!form) { return; }

    var dirty = false;
    form.addEventListener("input", function () { dirty = true; });
    form.addEventListener("change", function () { dirty = true; });
    // Saving is the one exit that keeps the edits, so it clears the flag before
    // the redirect that follows it.
    form.addEventListener("submit", function () { dirty = false; });

    window.addEventListener("beforeunload", function (event) {
      if (!dirty) { return undefined; }
      event.preventDefault();
      event.returnValue = "";
      return "";
    });

    var cancel = form.querySelector('a[href="/admin/channels"]');
    if (cancel) {
      cancel.addEventListener("click", function (event) {
        if (!dirty) { return; }
        if (!window.confirm(t("DiscardChanges"))) { event.preventDefault(); }
      });
    }
  })();

  /* ---------------------------------------------------------- list actions */

  document.addEventListener("click", function (event) {
    var target = event.target;
    if (!target.dataset) { return; }

    if (target.dataset.test) {
      request("POST", "/admin/api/channels/" + encodeURIComponent(target.dataset.test) + "/test")
        .then(function (res) { window.alert(testMessage(res)); })
        .catch(function (err) { window.alert(err.message); });
    }

    if (target.dataset.resetBreaker) {
      var name = target.dataset.resetBreaker;
      if (!window.confirm(fill(t("ConfirmResetBreaker"), [name]))) {
        return;
      }
      request("POST", "/admin/api/channels/" + encodeURIComponent(name) + "/breaker/reset")
        .then(function (res) {
          window.location.href = "/admin/channels?ok=" +
            encodeURIComponent(fill(t("BreakerResetFlash"), [name, res.was]));
        })
        .catch(function (err) { window.alert(err.message); });
    }

    if (target.dataset.delete) {
      var channel = target.dataset.delete;
      if (!window.confirm(fill(t("ConfirmDeleteChan"), [channel]))) {
        return;
      }
      request("DELETE", "/admin/api/channels/" + encodeURIComponent(channel))
        .then(function () {
          window.location.href = "/admin/channels?ok=" +
            encodeURIComponent(fill(t("DeletedFlash"), [channel]));
        })
        .catch(function (err) { window.alert(err.message); });
    }

    if (target.dataset.enableKey || target.dataset.disableKey) {
      var enableId = target.dataset.enableKey || target.dataset.disableKey;
      var turningOn = Boolean(target.dataset.enableKey);
      var toggledName = target.dataset.keyName || enableId;

      /* The flash is the point. This is the switch that decides what can
       * authenticate against the whole service, and it used to reload the page
       * and say nothing — so "did that work" was answered by noticing that a
       * small tag had changed colour. */
      request("POST", "/admin/api/keys/" + encodeURIComponent(enableId),
              { enabled: turningOn })
        .then(function () {
          var flash = turningOn ? t("KeyStateEnabled") : t("KeyStateDisabled");
          window.location.href = "/admin/keys?ok=" + encodeURIComponent(fill(flash, [toggledName]));
        })
        .catch(function (err) { window.alert(err.message); });
    }

    if (target.dataset.deleteKey) {
      var keyName = target.dataset.keyName;
      if (!window.confirm(fill(t("ConfirmDeleteKey"), [keyName]))) {
        return;
      }
      request("DELETE", "/admin/api/keys/" + encodeURIComponent(target.dataset.deleteKey))
        .then(function () {
          window.location.href = "/admin/keys?ok=" +
            encodeURIComponent(fill(t("DeletedFlash"), [keyName]));
        })
        .catch(function (err) { window.alert(err.message); });
    }

    if (target.dataset.replay) {
      var id = target.dataset.replay;
      if (!window.confirm(fill(t("ConfirmReplay"), [id]))) {
        return;
      }
      request("POST", "/admin/api/deliveries/" + encodeURIComponent(id) + "/replay")
        .then(function () {
          window.location.href = "/admin/deliveries?ok=" +
            encodeURIComponent(fill(t("ReplayedFlash"), [id]));
        })
        .catch(function (err) { window.alert(err.message); });
    }
  });

  /* -------------------------------------------------------------- API keys */

  var createKey = document.getElementById("create-key");
  if (createKey) {
    createKey.addEventListener("click", function () {
      var input = document.getElementById("key-name");
      var name = input.value.trim();
      if (!name) {
        window.alert(t("KeyNameRequired"));
        return;
      }

      request("POST", "/admin/api/keys", { name: name })
        .then(function (res) {
          input.value = "";
          // Deliberately no reload here. The token exists only in this response,
          // and a reload is how it would be lost — the list is refreshed when
          // the dialog closes instead.
          showToken(res.token);
        })
        .catch(function (err) { window.alert(err.message); });
    });
  }

  /* The token is shown in a dialog and never put in the URL, a log line or the
   * page's own HTML: it exists in this variable and on the clipboard, and
   * reloading the page discards it, which is the intended lifetime. */
  var pendingToken = null;

  function showToken(token) {
    pendingToken = token;
    var dialog = document.getElementById("token-dialog");
    if (!dialog) {
      // No dialog support: the token is still the thing the operator needs, so
      // it goes somewhere they can read it rather than nowhere.
      window.prompt(t("CopyTokenPrompt"), token);
      return;
    }
    document.getElementById("token-value").textContent = token;
    dialog.showModal();
  }

  var copyToken = document.getElementById("copy-token");
  if (copyToken) {
    copyToken.addEventListener("click", function () {
      if (pendingToken) { copyText(pendingToken, copyToken); }
    });
  }

  var closeToken = document.getElementById("close-token");
  if (closeToken) {
    closeToken.addEventListener("click", function () {
      pendingToken = null;
      var dialog = document.getElementById("token-dialog");
      if (dialog) { dialog.close(); }
      window.location.reload();
    });
  }

  /* ------------------------------------------------------------ first run */

  /* The setup form is not a state-changing request to an existing session —
   * there is no session yet — so it does not go through request(), which
   * attaches the CSRF header the server requires on everything else. This is
   * the one POST that does not need it, because there is nothing to forge: the
   * account it creates is the one every later request is checked against. */
  var setupForm = document.getElementById("setup-form");
  if (setupForm) {
    setupForm.addEventListener("submit", function (event) {
      event.preventDefault();

      var username = document.getElementById("username").value.trim();
      var password = document.getElementById("password").value;
      var confirm = document.getElementById("confirm").value;

      if (password !== confirm) {
        window.alert(t("PasswordsNoMatch"));
        return;
      }

      fetch("/admin/api/setup", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "same-origin",
        body: JSON.stringify({ username: username, password: password })
      }).then(function (res) {
        return res.json().then(function (body) {
          if (!res.ok) {
            throw new Error(body.message || t("SetupFailed"));
          }
          return body;
        });
      }).then(function () {
        /* The checklist, not the channel list. The channel list is a page for
         * somebody who already knows what this service is, and the person who
         * just created the first account does not. */
        window.location.href = "/admin/start";
      }).catch(function (err) {
        window.alert(err.message);
      });
    });
  }

  /* --------------------------------------------------------- API reference */

  /* The samples are all rendered and all visible; this only decides which one
   * to show once scripting is known to work. See the note in app.css. */
  var samples = document.getElementById("samples");
  if (samples) {
    var TAB_KEY = "notifyrelay.apiSample";

    function showSample(id) {
      var panels = samples.querySelectorAll("[data-sample]");
      for (var i = 0; i < panels.length; i++) {
        panels[i].classList.toggle("on", panels[i].dataset.sample === id);
      }
      var tabs = samples.querySelectorAll("[data-sample-tab]");
      for (var j = 0; j < tabs.length; j++) {
        tabs[j].classList.toggle("on", tabs[j].dataset.sampleTab === id);
      }
    }

    /* A remembered tab is restored only if it still exists — a sample removed
     * from a later build should not leave the page with nothing showing. */
    var remembered = null;
    try { remembered = window.localStorage.getItem(TAB_KEY); } catch (e) { /* private mode */ }
    if (!remembered || !samples.querySelector('[data-sample="' + remembered + '"]')) {
      var first = samples.querySelector("[data-sample]");
      remembered = first ? first.dataset.sample : null;
    }

    samples.classList.add("ready");
    if (remembered) { showSample(remembered); }

    samples.addEventListener("click", function (event) {
      var target = event.target;
      if (!target.dataset) { return; }

      if (target.dataset.sampleTab) {
        showSample(target.dataset.sampleTab);
        try { window.localStorage.setItem(TAB_KEY, target.dataset.sampleTab); } catch (e) { /* ignore */ }
        return;
      }

      if (target.dataset.copy) {
        var panel = samples.querySelector('[data-sample="' + target.dataset.copy + '"]');
        var code = panel ? panel.querySelector("code") : null;
        if (code) { copyText(code.textContent, target); }
      }
    });
  }

  /* copyText puts text on the clipboard and flashes the button.
   *
   * navigator.clipboard is only defined in a secure context, and this UI is
   * commonly reached over plain HTTP on an internal address — which is exactly
   * the deployment the API page is documenting. So the modern API is tried
   * first and the old selection trick is the fallback; without it the button
   * would silently do nothing on the addresses it is most likely to be used
   * from. */
  function copyText(text, button) {
    function done() {
      var was = button.textContent;
      button.classList.add("done");
      button.textContent = "✓";
      window.setTimeout(function () {
        button.classList.remove("done");
        button.textContent = was;
      }, 1200);
    }

    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text).then(done, function () { legacyCopy(text, done); });
      return;
    }
    legacyCopy(text, done);
  }

  function legacyCopy(text, done) {
    var area = document.createElement("textarea");
    area.value = text;
    area.setAttribute("readonly", "");
    area.style.position = "fixed";
    area.style.top = "-1000px";
    document.body.appendChild(area);
    area.select();
    try {
      document.execCommand("copy");
      done();
    } catch (e) {
      window.prompt(t("CopyManualPrompt"), text);
    }
    document.body.removeChild(area);
  }

  /* -------------------------------------------------------------- catalog */

  /* The catalogue is embedded so the form can resolve a parameter's type
   * without a round trip. It is the same document /api/v1/channels serves. */
  var catalogNode = document.getElementById("catalog");
  var catalog = [];
  if (catalogNode) {
    try { catalog = JSON.parse(catalogNode.textContent); } catch (e) { catalog = []; }
  }

  function catalogFor(type) {
    for (var i = 0; i < catalog.length; i++) {
      if (catalog[i].type === type) { return catalog[i]; }
    }
    return null;
  }
})();
