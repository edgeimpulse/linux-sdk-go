# Edge Impulse Linux SDK for Go

This library lets you run machine learning models and collect sensor data on Linux machines using Go. This SDK is part of [Edge Impulse](https://www.edgeimpulse.com) where we enable developers to create the next generation of intelligent device solutions with embedded machine learning. [Start here to learn more and train your first model](https://docs.edgeimpulse.com).

## Installation guide

1. Install [Go 1.15](https://golang.org/dl/) or higher.
1. Clone this repository:

    ```
    $ git clone https://github.com/edgeimpulse/linux-sdk-go
    ```

1. Find the example that you want to build and run `go build`:

    ```
    $ cd cmd/eimclassify
    $ go build
    ```

1. Run the example:

    ```
    $ ./eimclassify
    ```

    And follow instructions.

1. This SDK is also published to pkg.go.dev, so you can pull the package from there too.

## Collecting data

Before you can classify data you'll first need to collect it. If you want to collect data from the camera or microphone on your system you can use the Edge Impulse CLI, and if you want to collect data from different sensors (like accelerometers or proprietary control systems) you can do so in a few lines of code.

### Collecting data from the camera or microphone

1. Install the Edge Impulse CLI for Linux:

    ```
    $ npm install edge-impulse-linux -g --unsafe-perm
    ```

1. Start the CLI and follow the instructions:

    ```
    $ edge-impulse-linux
    ```

1. That's it. Your device is now connected to Edge Impulse and you can capture data from the camera and microphone.

### Collecting data from other sensors

To collect data from other sensors you'll need to write some code to collect the data from an external sensor, wrap it in the Edge Impulse Data Acquisition format, and upload the data to the Ingestion service. [Here's an end-to-end example](https://github.com/edgeimpulse/linux-sdk-go/blob/master/cmd/eimcollect/main.go).

## Classifying data

To classify data (whether this is from the camera, the microphone, or a custom sensor) you'll need a model file. This model file contains all signal processing code, classical ML algorithms and neural networks - and typically contains hardware optimizations to run as fast as possible. To grab a model file:

1. Train your model in Edge Impulse.
1. Install the Edge Impulse CLI:

    ```
    $ npm install edge-impulse-linux -g --unsafe-perm
    ```

1. Download the model file via:

    ```
    $ edge-impulse-linux-runner --download modelfile.eim
    ```

    This downloads the file into `modelfile.eim`. (Want to switch projects? Add `--clean`)

Then you can start classifying realtime sensor data. We have examples for:

* [Audio](https://github.com/edgeimpulse/linux-sdk-go/blob/master/cmd/eimaudio/main.go) - grabs data from the microphone and classifies it in realtime.
* [Camera](https://github.com/edgeimpulse/linux-sdk-go/blob/master/cmd/eimimage/main.go) - grabs data from a webcam and classifies it in realtime.
* [Custom data](https://github.com/edgeimpulse/linux-sdk-go/blob/master/cmd/eimclassify/main.go) - classifies custom sensor data.
