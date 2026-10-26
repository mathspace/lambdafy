# The sample needs only the standard library; omit unused OpenEXR image codecs
# inherited by the full Python image (CVE-2026-42216).
FROM python:3-slim
# Bump to force an OS image rebuild for updated certificates and other fixes.
RUN echo cachebuster-1
COPY server.py /
CMD python /server.py
