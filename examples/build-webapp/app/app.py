from flask import Flask

app = Flask(__name__)


@app.get("/")
def root():
    return "hello from a cloudcompose-built image\n", 200


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=80)
