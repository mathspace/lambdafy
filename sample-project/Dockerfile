FROM python
# Bump to force an OS image rebuild for updated certificates and other fixes.
RUN echo cachebuster-1
COPY server.py /
CMD python /server.py
