#!/bin/bash
# ==============================================================================
# worker_supervisor.sh
# Supervisor loop untuk menjamin Go MRP Worker selalu hidup (Restart=always).
# Jika worker crash atau di-kill (kill -9), supervisor akan me-restart secara otomatis
# dalam jeda 1 detik untuk menjamin ketersediaan engine tanpa intervensi manual.
# ==============================================================================

WORKER_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKER_BIN="$WORKER_DIR/bin/mrp-worker"

echo "[Supervisor] ==============================================="
echo "[Supervisor] Starting Go MRP Worker Supervisor..."
echo "[Supervisor] Target Binary: $WORKER_BIN"
echo "[Supervisor] Supervisor PID: $$"
echo "[Supervisor] ==============================================="

if [ ! -f "$WORKER_BIN" ]; then
    echo "[Supervisor] ERROR: Binary $WORKER_BIN tidak ditemukan! Build terlebih dahulu."
    exit 1
fi

trap 'echo "[Supervisor] Menerima sinyal terminasi. Menghentikan supervisor dan child..."; kill $CHILD_PID 2>/dev/null; exit 0' SIGINT SIGTERM

RESTART_COUNT=0

while true; do
    echo "[Supervisor] [$(date '+%Y-%m-%d %H:%M:%S')] Spawning worker process..."
    "$WORKER_BIN" &
    CHILD_PID=$!
    echo "[Supervisor] Worker spawned dengan PID: $CHILD_PID"
    
    # Tunggu sampai child proses selesai/mati
    wait $CHILD_PID
    EXIT_CODE=$?
    
    RESTART_COUNT=$((RESTART_COUNT + 1))
    echo "[Supervisor] [$(date '+%Y-%m-%d %H:%M:%S')] PERINGATAN: Worker (PID: $CHILD_PID) berhenti dengan exit code $EXIT_CODE!"
    echo "[Supervisor] Total restart otomatis: $RESTART_COUNT kali. Me-restart dalam 1 detik..."
    sleep 1
done
