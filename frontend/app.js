// smolRAFT — Collaborative Drawing Board Client
(function () {
    "use strict";

    // --- Configuration ---
    const WS_URL = (location.protocol === "https:" ? "wss://" : "ws://") + location.host + "/ws";
    const RECONNECT_BASE_MS = 1000;
    const RECONNECT_MAX_MS = 16000;

    // --- DOM Elements ---
    const canvas = document.getElementById("canvas");
    const ctx = canvas.getContext("2d");
    const statusEl = document.getElementById("status-indicator");
    const colorPicker = document.getElementById("color-picker");
    const widthSlider = document.getElementById("width-slider");
    const clearBtn = document.getElementById("clear-btn");
    const userIdEl = document.getElementById("user-id");

    // --- State ---
    let ws = null;
    let reconnectDelay = RECONNECT_BASE_MS;
    let isDrawing = false;
    let currentStroke = null;
    const userId = "user-" + Math.random().toString(36).substring(2, 8);

    userIdEl.textContent = userId;

    // --- Canvas Setup ---
    function resizeCanvas() {
        const dpr = window.devicePixelRatio || 1;
        canvas.width = canvas.clientWidth * dpr;
        canvas.height = canvas.clientHeight * dpr;
        ctx.scale(dpr, dpr);
        ctx.lineCap = "round";
        ctx.lineJoin = "round";
    }

    window.addEventListener("resize", resizeCanvas);
    resizeCanvas();

    // --- Drawing ---
    function getPos(e) {
        const rect = canvas.getBoundingClientRect();
        if (e.touches && e.touches.length > 0) {
            return {
                x: e.touches[0].clientX - rect.left,
                y: e.touches[0].clientY - rect.top,
            };
        }
        return { x: e.clientX - rect.left, y: e.clientY - rect.top };
    }

    function startStroke(e) {
        e.preventDefault();
        isDrawing = true;
        const pos = getPos(e);
        currentStroke = {
            id: userId + "-" + Date.now() + "-" + Math.random().toString(36).substring(2, 6),
            points: [pos],
            color: colorPicker.value,
            width: parseFloat(widthSlider.value),
            userId: userId,
        };
    }

    function moveStroke(e) {
        if (!isDrawing || !currentStroke) return;
        e.preventDefault();
        const pos = getPos(e);
        currentStroke.points.push(pos);
        drawStrokeSegment(
            currentStroke.points[currentStroke.points.length - 2],
            pos,
            currentStroke.color,
            currentStroke.width
        );
    }

    function endStroke(e) {
        if (!isDrawing || !currentStroke) return;
        e.preventDefault();
        isDrawing = false;

        if (currentStroke.points.length > 1 && ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify(currentStroke));
        }
        currentStroke = null;
    }

    function drawStrokeSegment(from, to, color, width) {
        ctx.beginPath();
        ctx.strokeStyle = color;
        ctx.lineWidth = width;
        ctx.moveTo(from.x, from.y);
        ctx.lineTo(to.x, to.y);
        ctx.stroke();
    }

    function drawFullStroke(stroke) {
        if (!stroke.points || stroke.points.length < 2) return;
        ctx.beginPath();
        ctx.strokeStyle = stroke.color || "#00ff41";
        ctx.lineWidth = stroke.width || 3;
        ctx.moveTo(stroke.points[0].x, stroke.points[0].y);
        for (let i = 1; i < stroke.points.length; i++) {
            ctx.lineTo(stroke.points[i].x, stroke.points[i].y);
        }
        ctx.stroke();
    }

    // Mouse events
    canvas.addEventListener("mousedown", startStroke);
    canvas.addEventListener("mousemove", moveStroke);
    canvas.addEventListener("mouseup", endStroke);
    canvas.addEventListener("mouseleave", endStroke);

    // Touch events
    canvas.addEventListener("touchstart", startStroke, { passive: false });
    canvas.addEventListener("touchmove", moveStroke, { passive: false });
    canvas.addEventListener("touchend", endStroke, { passive: false });
    canvas.addEventListener("touchcancel", endStroke, { passive: false });

    // Clear button
    clearBtn.addEventListener("click", function () {
        ctx.clearRect(0, 0, canvas.clientWidth, canvas.clientHeight);
    });

    // --- WebSocket ---
    function setStatus(state, text) {
        statusEl.className = state;
        statusEl.textContent = "\u25CF " + text;
    }

    function connect() {
        setStatus("reconnecting", "CONNECTING...");

        ws = new WebSocket(WS_URL);

        ws.onopen = function () {
            setStatus("connected", "CONNECTED");
            reconnectDelay = RECONNECT_BASE_MS;
        };

        ws.onclose = function () {
            setStatus("disconnected", "DISCONNECTED");
            scheduleReconnect();
        };

        ws.onerror = function () {
            // onclose will fire after this
        };

        ws.onmessage = function (event) {
            // Messages may be newline-batched by the gateway
            var parts = event.data.split("\n");
            for (var i = 0; i < parts.length; i++) {
                var part = parts[i].trim();
                if (!part) continue;
                try {
                    var msg = JSON.parse(part);
                    handleMessage(msg);
                } catch (err) {
                    console.warn("failed to parse ws message:", err);
                }
            }
        };
    }

    function handleMessage(msg) {
        if (msg.type === "stroke") {
            drawFullStroke(msg.payload);
        } else if (msg.type === "error") {
            console.warn("server error:", msg.payload);
        }
    }

    function scheduleReconnect() {
        setStatus("reconnecting", "RECONNECTING...");
        setTimeout(function () {
            reconnectDelay = Math.min(reconnectDelay * 2, RECONNECT_MAX_MS);
            connect();
        }, reconnectDelay);
    }

    // --- Boot ---
    connect();
})();
