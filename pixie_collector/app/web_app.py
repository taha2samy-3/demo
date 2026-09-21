import logging

from flask import Flask, jsonify, render_template, request

from app.state import state

logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(name)s: %(message)s")
logger = logging.getLogger("web_app")

app = Flask(__name__, template_folder="templates")


@app.route("/")
def index():
    return render_template("index.html")


@app.route("/api/data")
def get_data():
    logs = state.get_logs()
    total_logs = len(logs)
    pii_count = sum(1 for log in logs if log.get("has_pii"))
    clean_count = total_logs - pii_count

    return jsonify({
        "status": "ok",
        "current_model": state.current_model_name,
        "summary": {
            "total_logs": total_logs,
            "pii_detected_count": pii_count,
            "clean_count": clean_count
        },
        "logs": logs
    })


@app.route("/api/set_model", methods=["POST"])
def set_model():
    req_data = request.get_json(force=True, silent=True) or {}
    new_model = req_data.get("model")

    if new_model in ("fast", "distilbert"):
        state.current_model_name = new_model
        return jsonify({
            "status": "ok",
            "message": f"Model switched to '{new_model}'",
            "current_model": state.current_model_name
        })
    else:
        return jsonify({
            "status": "error",
            "message": "Invalid model specified. Must be 'fast' or 'distilbert'"
        }), 400


if __name__ == "__main__":
    logger.info("Starting Flask web app on port 5000...")
    app.run(host="0.0.0.0", port=5000, debug=False)
