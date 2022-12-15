FROM python

COPY . /html
WORKDIR /html
CMD echo cli-port=@@LAMBDAFY_PORT@@ ; echo port=$PORT ; python -m http.server $PORT
