# Lambdafy

*lambdafy* makes it easy to deploy **any** docker image verbatim on AWS Lambda
that serves a website.

This frees you from the constraints of available AWS Lambda runtimes.

## Get Started

Download [the latest release
binary](https://github.com/mathspace/lambdafy/releases) for your
OS/architecture, extract it and put it in your path.

Create an example project in `mynewlambda` directory and follow its `Readme.md`.

```sh
lambdafy create-sample-project mynewlambda
```

## What's next?

*lambdafy* command has completely self contained help. Run each command with
`-h` to see the available subcommands. There are dedicated guide commands as
well.

`lambdafy example-spec` is a good place to start as it's well documented and
outlines the extent of capabilities of lambdafy.

## How does it work?

Lambdafy embeds a proxy inside of your docker image when you run `lambdafy make
image-name` (or as part of `lambdafy publish` command when using non-ECR image
names) and makes it the entrypoint of the image. The resulting image is hybrid:
it behaves like the original image when run outside of lambda environment and
behaves like a lambda function when run inside of lambda environment.

Inside of lambda environment, the lambdafy proxy executes the original
entrypoint and commands as a subprocess. The proxy then translates API Gateway
Proxy requests into HTTP requests and sends them to your application which must
listen on the port provided by the `PORT` environment variable. The proxy then
translates the HTTP response back into API Gateway Proxy response.
