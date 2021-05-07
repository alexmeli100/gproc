# Teleport assessment design document

## Goal
Implement a prototype job worker service that provides an API to run arbitrary
Linux processes.

## Architecture
The system is made up of 3 components. The worker library, the grpc api and the client/cli interface to run commands
* The worker library provides methods to start, stop and get the status of a running job.
A method is also provided to stream the output of a job. The api can also be queried for all previous output of a running job. The server also provides a stream for clients to listen to job output.
* The grpc api is a grpc service that sits on top of the worker library. It provides methods to start, stop and get the status of a running job remotely. 
* The client/cli is an interface for users to start, stop and get the status of a running job on a remote server.

## Worker library API
The worker library consists of 3 components. The supervisor, the job worker and the process.
### Supervisor API
The supervisor is responsible for managing all jobs in the system. It provides apis to start, stop, get the output and get the status of a job. 
* `Newsupervisor()` - Returns a new instance of a supervisor
* `Add(cmd string, args []string, opts...Options)` - Adds a new job without running it.
* `Start(id string)` - Starts a previously added job.
* `Stop(id string)` - Stops a running job by sending the job's process group a `SIGTERM` signal.
* `Signal(id string, sig syscall.Signal)` - Sends an os signal to a job's process group.
* `Run()` - Run the supervisor and listen for incoming job requests.
* `WatchOutput(id string, c chan *pb.JobOutput)` - Add an output to channel to subscribe to process output. All previous output, if any, from the process is replayed before sending new messages to the channel.
* `UnWatchOutput(id string, c chan *pb.JobOutput)` - Unsubscribe to the process output.
* `WatchStatus(id string, c chan *pb.JobStatus)` - Subscribe to state changes for the job's process on the given channel.
* `WatchStatus(id string, c chan *pb.JobStatus)` - Unsubscribe to state changes for job's process.

### Job API
`Job` manages a single process and it's output and status updates.
* `NewJob(p *Process)` - Returns a new instance of a job.
* `Start()` - Start the process assigned the job.
* `Stop()` - Stop the process assigned to the job.
* `Signal(sig syscall.Signal)` - Send an os signal to the process group assigned to the job.

## Process API
`Process` is a wrapper around `os/exec.Cmd`
* `NewProcess(name string, args []string, opts ...Options)` - Returns a new instance of a process given a command name and it's arguments. An optional list of options can also be specified.
* `Start()` - Starts the process and returns a channel the caller can listen on to get the final status.
* `Stop()` - Stops the process by sending a `SIGTERM` signal to it's process group.
* `Signal(sig syscall.Signal)` - Send an os signal to the process group.
* `Status()` - Get the current status of a process.
* `IsInitialState()` - true if the process is in the initial state (the process isn't started yet).
* `IsRunningState()` - true if the process is starting or running.
* `IsFinalState()` - true if the process is in a final state

## GRPC API
Please refer to the service proto file for the full definition of the grpc api
* `rpc Register (JobRequest) returns (JobStatus) {}` - Registers a job without starting it. Returns a unique job id for subsequent use with the other api methods. A status `Initial` is returned to indicate the job has been added.
* `rpc Start (StartRequest) returns (stream JobStatus) {}` - Start a job that was previously registered using the provided job id. Returns a stream of `JobStatus` for the client to listen to status updates. Returns a `NOT_FOUND` error if the provided job id doesn't exist in the system. It is not an error to call `Start(...)` for a running job or a job in a final state. The stream is closed when the job reaches a final state.
    * Calling `Start(...)` when the process is running simply returns the job status stream.
    * Calling `Start(...)` when the process is in a final state returns the final status and closes the stream.
* `rpc StatusStream (StatusStreamRequest) returns (stream JobStatus) {}` - `StatusStream(...)` is a convenience method that returns a stream for the client to listen to job status updates using the provided job id. Returns a `NOT_FOUND` error if the provided job id doesn't exist in the system. It works similary to `Start(...)` except it does not start the job if it is not running. A status of `Initial` is returned if the job wasn't started. 
* `rpc Status(StatusRequest) returns (JobStatus) {}` - `Status(...)` simply returns the current status of a job. Returns a `NOT_FOUND` error if the job doesn't exist in the system. It is recommended to use `Start(...)` or `StatusStream(...)` to get job status updates instead of polling `Status(...)` repeatedly.
* `rpc Stop(StopRequest) returns (JobStatus) {}` - Stops a running job with the provided job id and returns the final status. Returns a `NOT_FOUND` error if the provided job id doesn't exist. The api call does nothing if the job is not running. A status `Initial` is returned in this case.
* `rpc GetOuput(GetOutputRequest) returns (stream JobOutput) {}` - Returns the job output stream given the job id. A `NOT_FOUND` error is returned if the provided job id does not exist. 
    * If the job is running, `GetOutput(...)` replays all previous output if any, from the job before streaming new messages. The stream is closed when the job reaches a final state
    * If called when the process is in a final state, `GetOutput(...)` sends all job output and closes the stream.
    
## Client/CLI
The cli is the interface for the user to send commands to the server. Assuming the name of the cli executable is teleport, then the command format is: 
```shell
> teleport [OPTIONS] COMMAND [ARG...]
 ```
The full set of commands will be provided through the help message which can be invoked with:
```shell
> teleport --help
```

## Authentication/Authorization
Client and server communication must be encrypted and secure. mTLS authentication is used to achieve this. A tool will be provided to generate certificates for the client and server using the same CA. 

## Some Notes
* A docker container will be provided to easily start and run the grpc server.

