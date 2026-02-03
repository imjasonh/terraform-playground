FROM smallstep/step-ca:latest

USER root
# Create directories for the secret mounts
RUN mkdir -p /home/step/secrets /home/step/config
RUN chown -R step:step /home/step

USER step

# We will mount the secrets to these locations via Cloud Run config
ENV STEPPATH=/home/step

# The entrypoint command
CMD ["/usr/local/bin/step-ca", "/home/step/config/ca.json"]
