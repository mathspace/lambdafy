import os
import sys
from pprint import pprint
from io import StringIO
from wsgiref.simple_server import make_server


def app(environ, start_response):

    if environ["PATH_INFO"] == "/_lambdafy/sqs":
        start_response("200 OK", [("Content-Type", "text/html")])
        length = int(environ.get("CONTENT_LENGTH", "0"))
        body = StringIO(environ["wsgi.input"].read(length).decode("utf8"))
        print("Received SQS message:", body.getvalue(), file=sys.stderr)
        return [b""]

    start_response("200 OK", [("Content-Type", "text/plain")])
    print("Received HTTP request", file=sys.stderr)
    return [b"Greetings from lambdafy.\n"]


port = int(os.getenv("PORT", "8080"))
with make_server("", port, app) as httpd:
    print(f"Listening on port {port} ...", file=sys.stderr)
    httpd.serve_forever()
