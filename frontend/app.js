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
    const undoBtn = document.getElementById("undo-btn");
    const redoBtn = document.getElementById("redo-btn");
    const userIdEl = document.getElementById("user-id");

    // --- State ---
    let ws = null;
    let reconnectDelay = RECONNECT_BASE_MS;
    let isDrawing = false;
    let currentStroke = null;
    const userId = "user-" + Math.random().toString(36).substring(2, 8);

    // Committed stroke tracking for undo/redo
    var committedStrokes = [];   // ordered list of {id, points, color, width, userId}
    var hiddenStrokeIds = {};    // set of stroke IDs hidden by undo
    var myStrokeIds = [];        // IDs of strokes drawn by this user (for undo order)
    var myRedoStack = [];        // IDs available for redo

    userIdEl.textContent = userId;

    // --- Canvas Setup ---
    function resizeCanvas() {
        const dpr = window.devicePixelRatio || 1;
        canvas.width = canvas.clientWidth * dpr;
        canvas.height = canvas.clientHeight * dpr;
        ctx.scale(dpr, dpr);
        ctx.lineCap = "round";
        ctx.lineJoin = "round";
        redrawAll();
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

    function redrawAll() {
        ctx.clearRect(0, 0, canvas.clientWidth, canvas.clientHeight);
        for (var i = 0; i < committedStrokes.length; i++) {
            var s = committedStrokes[i];
            if (!hiddenStrokeIds[s.id]) {
                drawFullStroke(s);
            }
        }
    }

    // --- Undo / Redo ---
    function updateUndoRedoButtons() {
        // Undo is available if this user has visible strokes
        var canUndo = false;
        for (var i = myStrokeIds.length - 1; i >= 0; i--) {
            if (!hiddenStrokeIds[myStrokeIds[i]]) {
                canUndo = true;
                break;
            }
        }
        undoBtn.disabled = !canUndo;
        redoBtn.disabled = myRedoStack.length === 0;
    }

    function doUndo() {
        if (!ws || ws.readyState !== WebSocket.OPEN) return;
        // Find last visible stroke by this user
        var targetId = null;
        for (var i = myStrokeIds.length - 1; i >= 0; i--) {
            if (!hiddenStrokeIds[myStrokeIds[i]]) {
                targetId = myStrokeIds[i];
                break;
            }
        }
        if (!targetId) return;
        ws.send(JSON.stringify({
            id: userId + "-undo-" + Date.now(),
            type: "undo",
            targetId: targetId,
            points: [],
            color: "",
            width: 0,
            userId: userId
        }));
    }

    function doRedo() {
        if (!ws || ws.readyState !== WebSocket.OPEN) return;
        if (myRedoStack.length === 0) return;
        var targetId = myRedoStack[myRedoStack.length - 1];
        ws.send(JSON.stringify({
            id: userId + "-redo-" + Date.now(),
            type: "redo",
            targetId: targetId,
            points: [],
            color: "",
            width: 0,
            userId: userId
        }));
    }

    undoBtn.addEventListener("click", doUndo);
    redoBtn.addEventListener("click", doRedo);

    // Keyboard shortcuts
    document.addEventListener("keydown", function (e) {
        if ((e.ctrlKey || e.metaKey) && e.key === "z" && !e.shiftKey) {
            e.preventDefault();
            doUndo();
        } else if ((e.ctrlKey || e.metaKey) && (e.key === "y" || (e.key === "z" && e.shiftKey))) {
            e.preventDefault();
            doRedo();
        }
    });

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
            var payload = msg.payload;
            var eventType = payload.type || "stroke";

            if (eventType === "undo") {
                // Mark target stroke as hidden
                if (payload.targetId) {
                    hiddenStrokeIds[payload.targetId] = true;
                    // If it's our undo, push to redo stack
                    if (payload.userId === userId) {
                        myRedoStack.push(payload.targetId);
                    }
                    redrawAll();
                }
            } else if (eventType === "redo") {
                // Restore target stroke
                if (payload.targetId) {
                    delete hiddenStrokeIds[payload.targetId];
                    // If it's our redo, remove from redo stack
                    if (payload.userId === userId) {
                        var idx = myRedoStack.indexOf(payload.targetId);
                        if (idx !== -1) myRedoStack.splice(idx, 1);
                    }
                    redrawAll();
                }
            } else {
                // Normal stroke
                committedStrokes.push(payload);
                if (payload.userId === userId) {
                    myStrokeIds.push(payload.id);
                    // New stroke clears redo stack for this user
                    myRedoStack = [];
                }
                drawFullStroke(payload);
            }
            updateUndoRedoButtons();
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

    // --- Live Dashboard ---
    var dashboardEl = document.getElementById("dashboard");
    var dashboardNodesEl = document.getElementById("dashboard-nodes");
    var dashboardToggle = document.getElementById("dashboard-toggle");
    var dashboardClose = document.getElementById("dashboard-close");
    var dashboardInterval = null;
    var REPLICA_ENDPOINTS = [
        { id: "1", url: "/replica/1/status" },
        { id: "2", url: "/replica/2/status" },
        { id: "3", url: "/replica/3/status" },
        { id: "4", url: "/replica/4/status" }
    ];

    dashboardToggle.addEventListener("click", function () {
        dashboardEl.classList.toggle("hidden");
        if (!dashboardEl.classList.contains("hidden")) {
            pollDashboard();
            dashboardInterval = setInterval(pollDashboard, 2000);
        } else {
            if (dashboardInterval) clearInterval(dashboardInterval);
        }
    });

    dashboardClose.addEventListener("click", function () {
        dashboardEl.classList.add("hidden");
        if (dashboardInterval) clearInterval(dashboardInterval);
    });

    function pollDashboard() {
        REPLICA_ENDPOINTS.forEach(function (ep) {
            fetch(ep.url, { signal: AbortSignal.timeout(1500) })
                .then(function (r) { return r.json(); })
                .then(function (data) { renderNode(ep.id, data); })
                .catch(function () { renderNode(ep.id, null); });
        });
    }

    function renderNode(replicaNum, data) {
        var elId = "dash-node-" + replicaNum;
        var el = document.getElementById(elId);
        if (!el) {
            el = document.createElement("div");
            el.id = elId;
            el.className = "dash-node";
            dashboardNodesEl.appendChild(el);
        }

        if (!data) {
            el.innerHTML =
                '<div class="dash-node-header">' +
                    '<span class="dash-node-id">Replica ' + replicaNum + '</span>' +
                    '<span class="dash-state DOWN">DOWN</span>' +
                '</div>';
            return;
        }

        el.innerHTML =
            '<div class="dash-node-header">' +
                '<span class="dash-node-id">' + (data.nodeId || "node" + replicaNum) + '</span>' +
                '<span class="dash-state ' + (data.state || "FOLLOWER") + '">' + (data.state || "?") + '</span>' +
            '</div>' +
            '<div class="dash-node-stats">' +
                '<span>T:' + (data.term || 0) + '</span>' +
                '<span>Log:' + (data.logLength || 0) + '</span>' +
                '<span>Commit:' + (data.commitIndex || 0) + '</span>' +
                '<span>Ldr:' + (data.leaderId || "—") + '</span>' +
            '</div>';
    }

    // --- Boot ---
    updateUndoRedoButtons();
    connect();
})();
