## Requirements

- Docker (including docker client CLI tools).

- You need to have your environment configured to access AWS APIs
  (usually done via setting the appropriate `AWS_*` environmental
  variables).

- Ensure your AWS credentials allow you to perform the IAM actions
  specified in the first part of the example role printed by `lambdafy
  example-role` command.

## Run

Simply run `./run.sh` to:

- Build a simple busybox based docker image which responds with a static
  text for all incoming HTTP requests.

- Embed (aka lambdafy/`make`) the lambdafy proxy into the docker image.

- Create an ECR repository.

- Push the docker image.

- Create a new lambda function.

- Make it publicly available.
