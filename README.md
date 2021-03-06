# Gproc
A library and utility to run arbitrary linux processes on a remote server.

## Architecture
The system is made up of 3 components. The worker library, the grpc api and the client/cli interface to run commands
* The worker library provides methods to start, stop and get the status of a running job.
  A method is also provided to stream the output of a job. The api can also be queried for all previous output of a running job. The server also provides a stream for clients to listen to job output.
* The grpc api is a grpc service that sits on top of the worker library. It provides methods to start, stop and get the status of a running job remotely.
* The client/cli is an interface for users to start, stop and get the status of a running job on a remote server.

## Worker library API
The worker library consists of 2 components. The supervisor and the process.
### Supervisor API
The supervisor is responsible for managing all jobs in the system. It provides apis to start, stop, get the output and get the status of a job.
* `Newsupervisor()` - Returns a new instance of a supervisor
* `Start(cmd string, args []string)` - Starts a process and return a unique job id. The job id is generated by the supervisor.
* `Stop(id string)` - Stops a running process by sending the process group a `SIGTERM` signal.
* `Run()` - Run the supervisor and listen for incoming job requests.
* `WatchOutput(ctx context.Context, id string)` - Returns a read only channel for the caller to listen for output from the job. The channel is closed when the job has finished running. All previous output, if any, from the process is replayed before sending new messages to the channel.
* `GetOutput(id string)` - Get all output from the job's output buffer. Useful when getting the output of a finished job.
* `Status(id string)` - Returns the status of a process


## Process API
`Process` is a wrapper around `os/exec.Cmd`
* `NewProcess(name string, args []string)` - Returns a new instance of a process given a command name and it's arguments. An optional list of options can also be specified.
* `Start()` - Starts the process and returns immediately. Client should call `Wait()` to wait for the process to finish running and get the final status.
* `Stop()` - Stops the process by sending a `SIGTERM` signal to it's process group.
* `Signal(sig syscall.Signal)` - Send an os signal to the process group.
* `Status()` - Get the current status of a process.
* `Wait()` - Wait for the process to finish running and return the final status
* `Done()` - Return the `done` channel for the process. This channel is closed when the process has finished running.

## GRPC API
Please refer to the service proto file for the full definition of the grpc api
* `rpc Start (JobRequest) returns (JobStatus) {}` - Start a process and return a `Jobstatus`. `JobStatus` also contains the unique job id generated by the server. The client should use this id with the rest of the rpc calls to identify the job.
* `rpc Status(StatusRequest) returns (JobStatus) {}` - `Status(...)` returns the current status of a job. Returns a `NOT_FOUND` error if the job doesn't exist in the system.
* `rpc Stop(StopRequest) returns (JobStatus) {}` - Stops a running process with the provided job id and returns the final status. Returns a `NOT_FOUND` error if the provided job id doesn't exist.
* `rpc GetOuput(GetOutputRequest) returns (stream JobOutput) {}` - Returns the process output stream given the job id. A `NOT_FOUND` error is returned if the provided job id does not exist.
    * If the job is running, `GetOutput(...)` replays all previous output if any, from the job before streaming new messages. The stream is closed when the job reaches a final state
    * If called when the process is in a final state, `GetOutput(...)` sends all job output and closes the stream.


## Client/CLI
The cli is the interface for the user to send commands to the server. The user must provide the client certificate, client key, CA certificate and server address to run the command. Assuming the name of the cli executable is teleport, then the command format is:
```shell
> gproc --cacert="/path/to/ca.cert" \
        --key="/path/to/client.key" \ 
        --cert="/path/to/client.cert" \
        --addr="server address" 
        COMMAND [OPTIONS] [ARG...]
 ```
`[Options]` is the set of flags or options supported by the cli and `[ARG...]` is the command name and arguments of the command the user wishes to run on the remote system. Example flow with server address `localhost:3000` and `ping` executable below.
### start
`start` starts a process on the server. An id identify the process is returned to be used with the other commands. Specify the `-l` flag to listen for process output after starting the process. This will return a job id of `job1` for example.
```shell
> gproc --cacert="ca.cert" --key="client.key" --cert="client.cert" --addr="localhost:3000" \
           start -l ping localhost
```
### status
`status` returns the status of a job. Specify the job id returned from the `start` command to run `status`.
```shell
> gproc --cacert="ca.cert" --key="client.key" --cert="client.cert" --addr="localhost:3000" status job1
```
### output
`output` displays the output from a process. Specify the job id returned from the `start` command to run `output`.
```shell
> gproc --cacert="ca.cert" --key="client.key" --cert="client.cert" --addr="localhost:3000" output job1
```
### stop
`stop` stops a process running on the server. Specify the job id returned from the `start` command to run `stop`.
```shell
> gproc --cacert="ca.cert" --key="client.key" --cert="client.cert" --addr="localhost:3000" stop job1
```
The full set of commands can be seen using the `--help` flag:
```shell
> gproc --help
```

## Authentication/Authorization
Client and server communication must be encrypted and secure. mTLS authentication is used to achieve this. A tool will be provided to generate certificates for the client and server using the same CA. The client certificates are each generated with a unique common name so the server can differentiate between different clients.
### Authorization
When starting a job, the server assigns the job to the client using the common name of it's cerficate. A client can stop, get the status and get the output of a job it owns. Attempting to perform any of these operations for a job not owned by the client results in an `NOT_FOUND` error.
