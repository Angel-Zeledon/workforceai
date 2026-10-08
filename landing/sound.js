/*
 * Synthesized sound for the hero stage (Web Audio, no files, no dependencies).
 * Off by default; the visitor turns effects and music on with the stage buttons and the
 * choice is remembered in localStorage. Audio only starts after that click (autoplay rules).
 *
 *   StageSound.sfx("delegation")   one-shot effect (ignored while effects are off)
 *   StageSound.hold(true|false)    suspend/resume everything (stage paused or off screen)
 */
(function () {
  "use strict";
  var KEY = "aiwos-sound";
  var prefs = load();
  var ctx = null, master, fxBus, musicBus, echo, held = false, music = null;

  function load() {
    try { return JSON.parse(localStorage.getItem(KEY)) || { fx: false, music: false }; } catch (e) { return { fx: false, music: false }; }
  }
  function save() { try { localStorage.setItem(KEY, JSON.stringify(prefs)); } catch (e) { /* private mode */ } }

  function boot() {
    if (ctx) return true;
    var AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return false;
    ctx = new AC();
    master = ctx.createGain(); master.gain.value = 0.55;
    var comp = ctx.createDynamicsCompressor();
    comp.threshold.value = -18; comp.ratio.value = 3;
    master.connect(comp); comp.connect(ctx.destination);
    fxBus = ctx.createGain(); fxBus.gain.value = 0.5; fxBus.connect(master);
    musicBus = ctx.createGain(); musicBus.gain.value = 0; musicBus.connect(master);
    // Soft feedback echo shared by plucks and the arpeggio: the "lo-fi room".
    echo = ctx.createDelay(1); echo.delayTime.value = 0.32;
    var fb = ctx.createGain(); fb.gain.value = 0.28;
    var tone = ctx.createBiquadFilter(); tone.type = "lowpass"; tone.frequency.value = 1800;
    echo.connect(tone); tone.connect(fb); fb.connect(echo); tone.connect(master);
    return true;
  }

  // One enveloped oscillator note.
  function note(freq, at, dur, opt) {
    opt = opt || {};
    var o = ctx.createOscillator(), g = ctx.createGain();
    o.type = opt.type || "triangle";
    o.frequency.setValueAtTime(freq, at);
    if (opt.to) o.frequency.exponentialRampToValueAtTime(opt.to, at + dur);
    var peak = opt.gain || 0.2, a = opt.attack || 0.006;
    g.gain.setValueAtTime(0.0001, at);
    g.gain.exponentialRampToValueAtTime(peak, at + a);
    g.gain.exponentialRampToValueAtTime(0.0001, at + dur);
    var out = g;
    if (opt.cutoff) {
      var f = ctx.createBiquadFilter(); f.type = "lowpass"; f.frequency.value = opt.cutoff;
      g.connect(f); out = f;
    }
    o.connect(g);
    out.connect(opt.bus || fxBus);
    if (opt.wet) { var w = ctx.createGain(); w.gain.value = opt.wet; out.connect(w); w.connect(echo); }
    o.start(at); o.stop(at + dur + 0.05);
  }

  function noise(at, dur, gain, cutoff, bus) {
    var len = Math.max(1, Math.floor(ctx.sampleRate * dur));
    var buf = ctx.createBuffer(1, len, ctx.sampleRate), d = buf.getChannelData(0);
    for (var i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
    var s = ctx.createBufferSource(), g = ctx.createGain(), f = ctx.createBiquadFilter();
    s.buffer = buf; f.type = "bandpass"; f.frequency.value = cutoff; f.Q.value = 0.8; g.gain.value = gain;
    s.connect(f); f.connect(g); g.connect(bus || fxBus); s.start(at);
  }

  var hz = function (midi) { return 440 * Math.pow(2, (midi - 69) / 12); };

  // Effects: short, soft and pitched to the music key (C major), so they sit on top of it.
  var FX = {
    type: function (t) { noise(t, 0.025, 0.08, 3200); },
    send: function (t) { noise(t, 0.03, 0.25, 2400); note(hz(76), t + 0.02, 0.18, { to: hz(83), gain: 0.12, type: "sine" }); },
    delegation: function (t) { note(hz(72), t, 0.35, { gain: 0.16, wet: 0.35 }); note(hz(79), t + 0.08, 0.45, { gain: 0.14, wet: 0.35 }); },
    consult: function (t) { note(hz(67), t, 0.16, { to: hz(79), type: "sine", gain: 0.16 }); },
    answer: function (t) { note(hz(79), t, 0.3, { gain: 0.13, wet: 0.3 }); note(hz(76), t + 0.09, 0.4, { gain: 0.12, wet: 0.3 }); },
    complete: function (t) { note(hz(84), t, 0.9, { type: "sine", gain: 0.11, wet: 0.25 }); note(hz(96), t, 0.5, { type: "sine", gain: 0.03 }); },
    approval: function (t) {
      [0, 0.55].forEach(function (d) {
        note(hz(81), t + d, 0.7, { type: "sine", gain: 0.15, wet: 0.3 }); note(hz(86), t + d + 0.14, 0.9, { type: "sine", gain: 0.13, wet: 0.3 });
      });
    },
    click: function (t) { noise(t, 0.02, 0.3, 1800); },
    approved: function (t) { [72, 76, 79].forEach(function (m, i) { note(hz(m), t + i * 0.09, 0.6, { gain: 0.14, wet: 0.3 }); }); },
    report: function (t) { [72, 76, 79, 84].forEach(function (m, i) { note(hz(m), t + i * 0.11, 0.9 - i * 0.1, { gain: 0.13, wet: 0.35, type: i === 3 ? "sine" : "triangle" }); }); }
  };

  // ---- music: lo-fi loop at 74 bpm, Cmaj7 - Am7 - Dm7 - G7, pad + bass + arpeggio + soft hat ----
  var BEAT = 60 / 74;
  var CHORDS = [[48, 55, 59, 64], [45, 52, 55, 60], [50, 57, 60, 65], [43, 53, 59, 62]];
  function startMusic() {
    if (music) return;
    var step = 0, next = ctx.currentTime + 0.1;
    music = setInterval(function () {
      while (next < ctx.currentTime + 0.4) {
        var bar = Math.floor(step / 8) % CHORDS.length, ch = CHORDS[bar], eighth = step % 8;
        if (eighth === 0) {
          ch.forEach(function (m) { note(hz(m + 12), next, BEAT * 4, { type: "sawtooth", gain: 0.022, attack: 0.6, cutoff: 900, bus: musicBus }); });
          note(hz(ch[0] - 12), next, BEAT * 1.6, { type: "sine", gain: 0.16, attack: 0.02, bus: musicBus });
        }
        if (eighth === 4) note(hz(ch[0] - 12), next, BEAT * 1.4, { type: "sine", gain: 0.12, attack: 0.02, bus: musicBus });
        if (eighth % 2 === 1 || Math.random() < 0.35) {
          var m = ch[1 + Math.floor(Math.random() * 3)] + 24;
          note(hz(m), next, 0.5, { gain: 0.04, cutoff: 2600, wet: 0.5, bus: musicBus });
        }
        if (eighth % 2 === 1) noise(next, 0.04, 0.035, 7000, musicBus);
        next += BEAT / 2;
        step++;
      }
    }, 120);
    musicBus.gain.setTargetAtTime(0.8, ctx.currentTime, 1.2);
  }
  function stopMusic() {
    if (!music) return;
    clearInterval(music); music = null;
    musicBus.gain.setTargetAtTime(0, ctx.currentTime, 0.3);
  }

  function apply() {
    if (!ctx) return;
    if (prefs.music && !held) startMusic(); else stopMusic();
    if ((prefs.fx || prefs.music) && !held) ctx.resume(); else if (!music) ctx.suspend();
  }

  // A saved "on" preference can only start audio after a user gesture: resume on the first one.
  if (prefs.fx || prefs.music) {
    var wake = function () {
      document.removeEventListener("pointerdown", wake); document.removeEventListener("keydown", wake);
      if ((prefs.fx || prefs.music) && boot()) apply();
    };
    document.addEventListener("pointerdown", wake); document.addEventListener("keydown", wake);
  }

  window.StageSound = {
    get: function () { return { fx: prefs.fx, music: prefs.music }; },
    // Called from a click: the only moment browsers allow audio to start.
    set: function (kind, on) {
      prefs[kind] = on; save();
      if (on && !boot()) return;
      apply();
      if (kind === "fx" && on) FX.click(ctx.currentTime + 0.01);
    },
    sfx: function (name) {
      if (!prefs.fx || held || !ctx || ctx.state !== "running" || !FX[name]) return;
      FX[name](ctx.currentTime + 0.01);
    },
    hold: function (h) { held = h; apply(); }
  };
})();
