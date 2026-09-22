/* NotifyRelay operator UI.
 *
 * Two jobs: send state-changing requests with the header the server requires,
 * and keep the generated form's conditional fields in step with the values
 * above them. Everything else is server-rendered.
 */
(function () {
  "use strict";

  var CSRF_HEADER = "X-NotifyRelay-Admin";

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
          throw new Error(message);
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

      var marker = field.querySelector(".req");
      if (marker) {
        marker.hidden = !(applies && field.getAttribute("data-required") === "1");
      }

      var input = field.querySelector("input, select");
      if (input && input.hasAttribute("data-required")) {
        input.required = applies;
      }
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
        quota: quota
      };
    }

    channelForm.addEventListener("submit", function (event) {
      event.preventDefault();
      request("POST", "/admin/api/channels", collect())
        .then(function (res) {
          if (res && res.live === false) {
            say(result, "Saved, but the running service refused it: " + res.reload, false);
            return;
          }
          window.location.href = "/admin/channels?ok=" +
            encodeURIComponent("Saved " + document.getElementById("channel-name").value);
        })
        .catch(function (err) { say(result, err.message, false); });
    });

    var testButton = document.getElementById("test-form");
    if (testButton) {
      testButton.addEventListener("click", function () {
        var name = document.getElementById("channel-name").value;
        if (!name) {
          say(result, "Save the channel before testing it.", false);
          return;
        }
        request("POST", "/admin/api/channels/" + encodeURIComponent(name) + "/test")
          .then(function (res) {
            say(result, res.ok ? "OK — " + res.detail
                               : res.class + " — " + (res.error || res.detail), res.ok);
          })
          .catch(function (err) { say(result, err.message, false); });
      });
    }
  }

  /* ---------------------------------------------------------- list actions */

  document.addEventListener("click", function (event) {
    var target = event.target;
    if (!target.dataset) { return; }

    if (target.dataset.test) {
      request("POST", "/admin/api/channels/" + encodeURIComponent(target.dataset.test) + "/test")
        .then(function (res) {
          window.alert(res.ok ? "OK — " + res.detail
                              : res.class + " — " + (res.error || res.detail));
        })
        .catch(function (err) { window.alert(err.message); });
    }

    if (target.dataset.resetBreaker) {
      var name = target.dataset.resetBreaker;
      if (!window.confirm("Reset the breaker for " + name + "?\n\n" +
                          "This clears a protection that was switched on because the channel " +
                          "kept failing. It does not drain the queue or return any allowance.")) {
        return;
      }
      request("POST", "/admin/api/channels/" + encodeURIComponent(name) + "/breaker/reset")
        .then(function (res) {
          window.location.href = "/admin/channels?ok=" +
            encodeURIComponent(name + ": breaker was " + res.was + ", now closed");
        })
        .catch(function (err) { window.alert(err.message); });
    }

    if (target.dataset.delete) {
      var channel = target.dataset.delete;
      if (!window.confirm("Delete channel " + channel + "?\n\n" +
                          "Deliveries already queued for it will fail.")) {
        return;
      }
      request("DELETE", "/admin/api/channels/" + encodeURIComponent(channel))
        .then(function () {
          window.location.href = "/admin/channels?ok=" + encodeURIComponent("Deleted " + channel);
        })
        .catch(function (err) { window.alert(err.message); });
    }

    if (target.dataset.replay) {
      var id = target.dataset.replay;
      if (!window.confirm("Replay delivery " + id + "?\n\n" +
                          "It goes back to the queue with a fresh attempt budget and will be " +
                          "delivered again.")) {
        return;
      }
      request("POST", "/admin/api/deliveries/" + encodeURIComponent(id) + "/replay")
        .then(function () {
          window.location.href = "/admin/deliveries?ok=" + encodeURIComponent("Replayed " + id);
        })
        .catch(function (err) { window.alert(err.message); });
    }
  });

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
      window.prompt("Copy with Ctrl+C / Cmd+C:", text);
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
