/* Velm ambient overlays. One fixed canvas behind app content; the active
   effect is chosen by html[data-overlay]. Independent of colour theme.
   Effects are deliberately cheap: <= 120 particles, no shadows, 30fps cap,
   pauses when the tab is hidden, single static frame under reduced motion. */
(function () {
    "use strict";

    var canvas = null;
    var ctx = null;
    var rafId = 0;
    var running = "";
    var reduceMotion = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    var lastFrame = 0;
    var particles = [];

    function rand(min, max) { return min + Math.random() * (max - min); }

    function makeCanvas() {
        if (canvas) return;
        canvas = document.createElement("canvas");
        canvas.className = "ui-overlay-canvas";
        canvas.setAttribute("aria-hidden", "true");
        document.body.appendChild(canvas);
        ctx = canvas.getContext("2d");
        resize();
        window.addEventListener("resize", resize);
        document.addEventListener("visibilitychange", function () {
            if (document.hidden) { cancelAnimationFrame(rafId); rafId = 0; }
            else if (running && !reduceMotion) { rafId = requestAnimationFrame(tick); }
        });
    }

    function resize() {
        if (!canvas) return;
        canvas.width = window.innerWidth;
        canvas.height = window.innerHeight;
        seed(running);
    }

    function isDark() {
        var t = document.documentElement.dataset.theme;
        return t === "dark" || t === "purple" || t === "crimson";
    }

    function seed(effect) {
        particles = [];
        if (!ctx) return;
        var w = canvas.width, h = canvas.height, i;
        if (effect === "matrix") {
            for (i = 0; i < 90; i++) {
                particles.push({ x: rand(0, w), y: rand(-h, h), s: rand(4, 9), v: rand(14, 40), o: rand(0.12, 0.4), blur: Math.random() < 0.35 });
            }
        } else if (effect === "rainfall") {
            for (i = 0; i < 110; i++) {
                particles.push({ x: rand(0, w), y: rand(-h, h), l: rand(9, 26), v: rand(90, 220), o: rand(0.05, 0.2), blur: Math.random() < 0.3 });
            }
        } else if (effect === "fireflies") {
            for (i = 0; i < 44; i++) {
                particles.push({ x: rand(0, w), y: rand(0, h), r: rand(1.4, 3.4), vx: rand(-9, 9), vy: rand(-7, 7), o: rand(0.25, 0.8), ph: rand(0, 6.28), blur: Math.random() < 0.4 });
            }
        } else if (effect === "nature") {
            for (i = 0; i < 7; i++) {
                particles.push({ x: rand(0, w), y: rand(0, h), s: rand(7, 14), vx: rand(8, 26), vy: rand(-6, 6), o: rand(0.2, 0.42), kind: "butterfly", hue: Math.floor(rand(0, 3)), ph: rand(0, 6.28) });
            }
            for (i = 0; i < 3; i++) {
                particles.push({ x: rand(0, w), y: rand(0, h * 0.6), s: rand(9, 15), vx: rand(16, 34), vy: rand(-3, 3), o: rand(0.25, 0.45), kind: "bird" });
            }
        } else if (effect === "aurora") {
            particles.push({ t: 0 });
        }
    }

    function drawMatrix(dt) {
        var w = canvas.width, h = canvas.height, p, i;
        for (i = 0; i < particles.length; i++) {
            p = particles[i];
            p.y += p.v * dt;
            if (p.y > h + 12) { p.y = -12; p.x = rand(0, w); }
            ctx.globalAlpha = p.blur ? p.o * 0.55 : p.o;
            ctx.fillStyle = isDark() ? "#34d97f" : "#0d8a45";
            var snap = Math.round(p.x / 14) * 14;
            var snapY = Math.round(p.y / 14) * 14;
            ctx.fillRect(snap, snapY, p.s, p.s);
        }
    }

    function drawRainfall(dt) {
        var w = canvas.width, h = canvas.height, p, i;
        ctx.lineWidth = 1.1;
        for (i = 0; i < particles.length; i++) {
            p = particles[i];
            p.y += p.v * dt;
            p.x -= p.v * 0.16 * dt;
            if (p.y > h + 30) { p.y = -30; p.x = rand(0, w + 60); }
            ctx.globalAlpha = p.blur ? p.o * 0.5 : p.o;
            ctx.strokeStyle = isDark() ? "#8fb4d9" : "#5b7a9d";
            ctx.beginPath();
            ctx.moveTo(p.x, p.y);
            ctx.lineTo(p.x - p.l * 0.16, p.y - p.l);
            ctx.stroke();
        }
    }

    function drawFireflies(now, dt) {
        var w = canvas.width, h = canvas.height, p, i, glow;
        for (i = 0; i < particles.length; i++) {
            p = particles[i];
            p.x += p.vx * dt; p.y += p.vy * dt;
            if (p.x < -10) p.x = w + 10; if (p.x > w + 10) p.x = -10;
            if (p.y < -10) p.y = h + 10; if (p.y > h + 10) p.y = -10;
            glow = 0.45 + 0.55 * Math.abs(Math.sin(now / 1400 + p.ph));
            ctx.globalAlpha = p.o * glow;
            ctx.fillStyle = "#ffd166";
            if (p.blur) {
                ctx.beginPath(); ctx.arc(p.x, p.y, p.r * 3, 0, 6.283); ctx.globalAlpha = p.o * glow * 0.3; ctx.fill();
            }
            ctx.beginPath(); ctx.arc(p.x, p.y, p.r, 0, 6.283); ctx.fill();
        }
    }

    function drawNature(dt, now) {
        var w = canvas.width, h = canvas.height, p, i, flap, wing;
        var hues = ["#e8a13a", "#d96f4e", "#7f9fd9"];
        for (i = 0; i < particles.length; i++) {
            p = particles[i];
            p.x += p.vx * dt; p.y += p.vy * dt + Math.sin(now / 900 + p.ph) * 0.3;
            if (p.x > w + 30) { p.x = -30; p.y = rand(0, h); }
            if (p.y < -20) p.y = h + 20; if (p.y > h + 20) p.y = -20;
            ctx.globalAlpha = p.o;
            if (p.kind === "butterfly") {
                flap = Math.abs(Math.sin(now / 220 + p.ph));
                ctx.fillStyle = hues[p.hue];
                wing = p.s * (0.35 + 0.4 * flap);
                ctx.beginPath(); ctx.ellipse(p.x - p.s * 0.5, p.y, wing, p.s * 0.45, -0.4, 0, 6.283); ctx.fill();
                ctx.beginPath(); ctx.ellipse(p.x + p.s * 0.5, p.y, wing, p.s * 0.45, 0.4, 0, 6.283); ctx.fill();
                ctx.fillStyle = "#5b4426";
                ctx.beginPath(); ctx.ellipse(p.x, p.y, p.s * 0.12, p.s * 0.5, 0, 0, 6.283); ctx.fill();
            } else {
                ctx.strokeStyle = isDark() ? "#cbd5e1" : "#3f4a55";
                ctx.lineWidth = 1.6;
                ctx.beginPath();
                ctx.moveTo(p.x - p.s, p.y);
                ctx.quadraticCurveTo(p.x - p.s * 0.4, p.y - p.s * 0.7, p.x, p.y);
                ctx.quadraticCurveTo(p.x + p.s * 0.4, p.y - p.s * 0.7, p.x + p.s, p.y);
                ctx.stroke();
            }
        }
    }

    function drawAurora(now) {
        var w = canvas.width, h = canvas.height;
        ctx.clearRect(0, 0, w, h);
        var bands = [
            ["rgba(61, 219, 180, 0.30)", 0.22],
            ["rgba(91, 143, 217, 0.26)", 0.34],
            ["rgba(160, 107, 217, 0.22)", 0.48],
            ["rgba(61, 219, 184, 0.16)", 0.64]
        ];
        var b, i, yBase, phase;
        for (i = 0; i < bands.length; i++) {
            b = bands[i];
            phase = now / 9000 + i * 1.7;
            yBase = h * b[1] + Math.sin(phase) * 26;
            var grad = ctx.createLinearGradient(0, yBase - 130, 0, yBase + 60);
            grad.addColorStop(0, "rgba(0,0,0,0)");
            grad.addColorStop(0.55, b[0]);
            grad.addColorStop(1, "rgba(0,0,0,0)");
            ctx.globalAlpha = 1;
            ctx.fillStyle = grad;
            ctx.beginPath();
            ctx.moveTo(0, yBase);
            for (var x = 0; x <= w; x += 40) {
                ctx.lineTo(x, yBase + Math.sin(x / 260 + phase * 2) * 34);
            }
            ctx.lineTo(w, 0); ctx.lineTo(0, 0);
            ctx.closePath();
            ctx.fill();
        }
        ctx.globalAlpha = 0.5;
        ctx.fillStyle = "#dfe8ff";
        for (i = 0; i < particles.length * 0; i++) { /* stars drawn once below */ }
        if (!particles.stars) {
            particles.stars = [];
            for (i = 0; i < 40; i++) particles.stars.push({ x: rand(0, w), y: rand(0, h * 0.55), r: rand(0.5, 1.4), o: rand(0.2, 0.7) });
        }
        for (i = 0; i < particles.stars.length; i++) {
            ctx.globalAlpha = particles.stars[i].o;
            ctx.beginPath(); ctx.arc(particles.stars[i].x, particles.stars[i].y, particles.stars[i].r, 0, 6.283); ctx.fill();
        }
    }

    function tick(now) {
        rafId = 0;
        if (!ctx || !running) return;
        var dt = Math.min((now - lastFrame) / 1000, 0.1);
        if (dt < 1 / 45) { rafId = requestAnimationFrame(tick); return; }  // cap ~45fps
        lastFrame = now;
        if (running !== "aurora") ctx.clearRect(0, 0, canvas.width, canvas.height);
        if (running === "matrix") drawMatrix(dt);
        else if (running === "rainfall") drawRainfall(dt);
        else if (running === "fireflies") drawFireflies(now, dt);
        else if (running === "nature") drawNature(dt, now);
        else if (running === "aurora") drawAurora(now);
        rafId = requestAnimationFrame(tick);
    }

    function apply(effect) {
        if (!effect || effect === "none" || !validEffects[effect]) {
            stop(); return;
        }
        makeCanvas();
        if (running !== effect) { running = effect; seed(effect); }
        if (reduceMotion) { running = effect; lastFrame = 0; tick(1); running = effect; cancelAnimationFrame(rafId); rafId = 0; return; }
        if (!rafId && !document.hidden) { lastFrame = performance.now(); rafId = requestAnimationFrame(tick); }
    }

    function stop() {
        running = "";
        if (rafId) { cancelAnimationFrame(rafId); rafId = 0; }
        if (ctx) ctx.clearRect(0, 0, canvas.width, canvas.height);
    }

    var validEffects = { matrix: 1, nature: 1, rainfall: 1, fireflies: 1, aurora: 1 };

    document.addEventListener("velm:overlay", function (event) { apply(event.detail); });
    document.addEventListener("velm:theme", function () { if (running) seed(running); });

    apply(document.documentElement.dataset.overlay || "none");
})();
