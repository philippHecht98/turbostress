package main

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var powerMetrics = []string{"PkgWatt", "RAMWatt", "PkgTmp"}

func main() {
	logrus.SetOutput(os.Stderr)
	// defaults
	input := benchInput{
		loadStep:                   25,
		repititions:                1,
		loadDurationBeforeMeasures: time.Duration(5 * time.Second),
		threads:                    runtime.NumCPU(),
		metrics:                    powerMetrics,
		repeat:                     10,
		durationBetweenMeasures:    time.Duration(1 * time.Second),
		method:                     "all",
		cpuInfo:                    false,
		ipsec:                      false,
		vm:                         true,
		maximize:                   true,
		suites:                     []string{"fluidanimate", "ferret"},
	}

	cmd := &cobra.Command{
		Long: `
This tool generates load and outputs computer power metrics for this load.
It requires adequate privileges(CAP_SYS_RAWIO, or simply run as ` + "`sudo`" + `) to read the metrics.

It combines CPU load generation using ` + "`stress-ng`" + ` and power metrics measurement using ` + "`turbostat`" + `.
For each load step from 0 to 100, a CPU load corresponding is started and multiple measures of power metrics are taken.
The value of each metric for each step is the mean of the multiple measurements. 
A final measure may be taken using ipsec feature of ` + "`stress-ng`" + ` to trigger advanced CPU instruction usage (AVX and so).

Progression messages are written to STDERR while results are written to STDOUT.
The two can be separated to build a CSV result file while displaying the progression on the console, ex: turbostress | tee results.csv
		`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				err error
				ci  string
			)
			if input.cpuInfo {
				ci, err = cpuInfo()
				if err != nil {
					return err
				}
			}

			_, err = os.Stdout.WriteString(ci + "\n#---\n")
			if err != nil {
				return err
			}

			err = bench(input, os.Stdout)
			if err != nil {
				return err
			}

			return err
		},
	}

	cmd.PersistentFlags().IntVar(&input.loadStep, "load-step", input.loadStep, "increment the stress load from 0 to 100 with this value")
	cmd.PersistentFlags().IntVar(&input.repititions, "repititions", input.repititions, "set the number of tests")
	cmd.PersistentFlags().DurationVar(&input.loadDurationBeforeMeasures, "load-duration-before-measures", input.loadDurationBeforeMeasures, "duration to wait between load start and measures")
	cmd.PersistentFlags().IntVar(&input.threads, "threads", input.threads, "number of threads to use for the load, defaults to the number of threads on the system")
	cmd.PersistentFlags().StringSliceVar(&input.metrics, "metrics", input.metrics, "turbostat columns to read")
	cmd.PersistentFlags().IntVar(&input.repeat, "repeat", input.repeat, "measures are repeated with this value and the measure is the mean of all repetitions")
	cmd.PersistentFlags().DurationVar(&input.durationBetweenMeasures, "duration-between-measures", input.durationBetweenMeasures, "the duration to wait between two measures")
	cmd.PersistentFlags().StringVar(&input.method, "method", input.method, "the method to use to generate the load. See stress-ng cpu-method flag")
	cmd.PersistentFlags().BoolVar(&input.cpuInfo, "cpu-info", input.cpuInfo, "output CPU info before results")
	cmd.PersistentFlags().BoolVar(&input.ipsec, "ipsec", input.ipsec, "launch ipsec test to trigger advanced CPU instructions. See stress-ng ipsec-mb flag")
	cmd.PersistentFlags().BoolVar(&input.vm, "vm", input.vm, "launch VM test. See stress-ng vm flag")
	cmd.PersistentFlags().BoolVar(&input.maximize, "maximize", input.maximize, "launch a stress maximizing stressors values. See stress-ng maximize flag")

	err := cmd.Execute()
	if err != nil {
		logrus.Fatal(err)
	}
}

type benchInput struct {
	loadStep                   int
	repititions                int
	threads                    int
	loadDurationBeforeMeasures time.Duration
	metrics                    []string
	repeat                     int
	durationBetweenMeasures    time.Duration
	initialLoad                int
	method                     string
	cpuInfo                    bool
	ipsec                      bool
	vm                         bool
	maximize                   bool
	suites                     []string
}

func (bi *benchInput) toString() string {
	return fmt.Sprintf("loadStep: %d, initialLoad: %d, method: %s",
		bi.loadStep, bi.initialLoad, bi.method)
}

func connectToHost() (net.Conn, error) {
	conn, err := net.Dial("tcp", "192.168.122.1:4444")
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func requestTesting(connection net.Conn, arguments benchInput, testcase string) error {
	logrus.Info("Requested to start a Test")

	msg := fmt.Sprintf("startTestReq %s\n", testcase)
	len, err := connection.Write([]byte(msg))

	// try receive acknowledge package
	acknowledge := make([]byte, 4)
	len, err = connection.Read(acknowledge)

	if err != nil || len != 4 || string(acknowledge) != "ack\n" {
		return errors.New("failed to receive acknowledge")
	}
	return nil
}

func waitForFinishingRecording(connection net.Conn) {
	logrus.Info("Waiting for finishing the recording")

	finish := make([]byte, 4)
	len, err := connection.Read(finish)

	if err != nil || len != 4 || string(finish) != "fin\n" {
		logrus.Error("Failed to receiving finish package")
	}
}

func finishTesting(connection net.Conn) {
	logrus.Info("Requested to finish a Test")

	msg := "finished recording\n"
	connection.Write([]byte(msg))
}

func stress(input benchInput, name string, conn net.Conn, stressFn func(load int, threads int) (*exec.Cmd, error)) error {
	var repitition = 0

	var load = input.initialLoad
	for {
		repitition = 0
		for {
			logrus.Infof("load_duration_before_measure: %ds, load: %d, threads: %d", int(input.loadDurationBeforeMeasures.Seconds()), load, input.threads)
			// initialize TCP Connection to Bare-Metal
			var function_name = name //strings.Split(runtime.FuncForPC(reflect.ValueOf(stressFn).Pointer()).Name(), ".")[1]

			logrus.Infoln(function_name)

			err := requestTesting(conn, input, fmt.Sprintf("%s/%d/%d", function_name, load, repitition))
			if err != nil {
				return err
			}

			stress, err := stressFn(load, input.threads)
			if err != nil {
				return err
			}

			done := make(chan error)
			go func() {
				//defer logrus.Error("stress-ng gone before end of measures, see stress-ng output for details")
				defer close(done)
				done <- stress.Wait()
			}()

			waitForFinishingRecording(conn)

			err = stress.Process.Kill()
			//stress.Process.Wait()
			/*
				if err != nil {
					logrus.Errorf("failed to kill process: %s", err.Error())
					return err
				}
			*/

			err = <-done
			if stress.ProcessState.ExitCode() != -1 {
				return fmt.Errorf("stress-ng was not terminated by a signal, EC: %d, err: %v", stress.ProcessState.ExitCode(), err)
			}
			repitition++
			if repitition >= input.repititions {
				break
			}
		}

		// increase load of stress test
		if load == 100 {
			logrus.Info("finished testing for this load")
			break
		}

		load += input.loadStep
		if load > 100 {
			load = 100
		}
	}
	return nil
}

func cpuStress(input benchInput, conn net.Conn) error {
	return stress(input, "CPUStress", conn, func(load int, threads int) (*exec.Cmd, error) {
		return stressNGCPUStress(load, threads, input.method)
	})
}

func vmStress(input benchInput, conn net.Conn) error {
	return stress(input, "VMStress", conn, func(load int, threads int) (*exec.Cmd, error) {
		return stressNGVMStress(load, threads)
	})
}

func ipsecStress(input benchInput, conn net.Conn) error {
	return stress(input, "ipsec", conn, func(_, threads int) (*exec.Cmd, error) {
		return stressNGIPSec(threads)
	})
}

func maximizeStress(input benchInput, conn net.Conn) error {
	return stress(input, "maximize", conn, func(_, threads int) (*exec.Cmd, error) {
		return stressNGMAximize(threads)
	})
}

func ioStress(input benchInput, conn net.Conn) error {
	return stress(input, "io", conn, func(_, threads int) (*exec.Cmd, error) {
		return stressNGIO(threads)
	})
}

func webserverStress(input benchInput, conn net.Conn) error {
	return stress(input, "webserver", conn, func(load, threads int) (*exec.Cmd, error) {
		return stressNGWebserver(load, threads)
	})
}

func fluidanimateStress(input benchInput, conn net.Conn) error {
	return stress(input, "fluidanimate", conn, func(load, threads int) (*exec.Cmd, error) {
		return stressFluidanimate(input)
	})
}

func ferretStress(input benchInput, conn net.Conn) error {
	return stress(input, "ferret", conn, func(load, threads int) (*exec.Cmd, error) {
		return stressFerret(threads)
	})
}

func blackscholesStress(input benchInput, conn net.Conn) error {
	return stress(input, "blackscholes", conn, func(load, threads int) (*exec.Cmd, error) {
		return stressBlackschole(threads)
	})
}

func streamclusterStress(input benchInput, conn net.Conn) error {
	return stress(input, "streamcluster", conn, func(load, threads int) (*exec.Cmd, error) {
		return stressStreamCluster(threads)
	})
}

func vipsStress(input benchInput, conn net.Conn) error {
	return stress(input, "vips", conn, func(load, threads int) (*exec.Cmd, error) {
		return stressVips(threads)
	})
}

func netstreamclusterStress(input benchInput, conn net.Conn) error {
	return stress(input, "netstreamcluster", conn, func(load, threads int) (*exec.Cmd, error) {
		return stressNetStreamCluster(threads)
	})
}

func netferretStress(input benchInput, conn net.Conn) error {
	return stress(input, "netferret", conn, func(load, threads int) (*exec.Cmd, error) {
		return stressNetFerrret(threads)
	})
}

func swaptionsStress(input benchInput, conn net.Conn) error {
	return stress(input, "swaptions", conn, func(load, threads int) (*exec.Cmd, error) {
		return stressSwaptions(threads)
	})
}

func bench(input benchInput, output io.Writer) error {
	//header
	header := append([]string{"test", "threads", "load"}, input.metrics...)
	err := write(header, output)
	if err != nil {
		return err
	}

	conn, err := connectToHost()
	if err != nil {
		return err
	}

	/*
		err = cpuStress(input, conn)
		if err != nil {
			return err
		}

		if input.ipsec {
			err = ipsecStress(input, conn)
			if err != nil {
				return err
			}
		}

		err = ioStress(input, conn)
		if err != nil {
			return err
		}

		input.initialLoad = 10
		if input.vm {
			err = vmStress(input, conn)
			if err != nil {
				return err
			}
		}

		input.initialLoad = 60
		err = webserverStress(input, conn)
		if err != nil {
			return err
		}

		input.initialLoad = 100
		if input.maximize {
			err = maximizeStress(input, conn)
			if err != nil {
				return err
			}
		}
	*/

	err = fluidanimateStress(input, conn)
	if err != nil {
		return err
	}

	err = ferretStress(input, conn)
	if err != nil {
		return err
	}

	err = blackscholesStress(input, conn)
	if err != nil {
		return err
	}

	err = streamclusterStress(input, conn)
	if err != nil {
		return err
	}

	err = vipsStress(input, conn)
	if err != nil {
		return err
	}

	err = swaptionsStress(input, conn)
	if err != nil {
		return err
	}

	err = netferretStress(input, conn)
	if err != nil {
		return err
	}

	err = netstreamclusterStress(input, conn)
	if err != nil {
		return err
	}

	finishTesting(conn)
	return nil
}

func cpuInfo() (string, error) {
	infoBytes, err := ioutil.ReadFile("/proc/cpuinfo")
	return string(infoBytes), err
}

func write(data []string, writer io.Writer) error {
	line := strings.Join(data, ",")
	_, err := writer.Write([]byte(line + "\n"))
	return err
}

func parsec(args ...string) (*exec.Cmd, error) {
	cmd := exec.Command("../parsec/parsec-3.0/bin/parsecmgmt",
		args...,
	)
	logrus.Info(cmd.Args)
	cmd.Stdout = nil
	cmd.Stderr = nil
	err := cmd.Start()
	if err != nil {
		return nil, err
	}
	return cmd, nil
}

func stressNG(args ...string) (*exec.Cmd, error) {
	cmd := exec.Command("stress-ng", args...)
	logrus.Info(cmd.Args)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	err := cmd.Start()
	if err != nil {
		return nil, err
	}
	return cmd, nil
}

func stressNGCPUStress(load, threads int, method string) (*exec.Cmd, error) {
	return stressNG("-l", fmt.Sprintf("%d", load), "-c", fmt.Sprintf("%d", threads), "--cpu-method", method)
}

func stressNGIPSec(threads int) (*exec.Cmd, error) {
	return stressNG("--ipsec-mb", fmt.Sprintf("%d", threads))
}

func stressNGVMStress(load, threads int) (*exec.Cmd, error) {
	return stressNG("--vm", fmt.Sprintf("%d", threads), "--vm-bytes", fmt.Sprintf("%d%%", load), "--vm-method", "all")
}

func stressNGMAximize(threads int) (*exec.Cmd, error) {
	return stressNG("--cpu", fmt.Sprintf("%d", threads), "--vm", fmt.Sprintf("%d", threads), "--maximize")
}

func stressNGIO(threads int) (*exec.Cmd, error) {
	return stressNG("--iomix", fmt.Sprintf("%d", threads))
}

func stressNGWebserver(load, threads int) (*exec.Cmd, error) {
	return stressNG(
		"--cpu", fmt.Sprintf("%d", threads/2),
		"--cpu-load", fmt.Sprintf("%d", load),
		"--vm", fmt.Sprintf("%d", threads/2),
		"--vm-bytes", fmt.Sprintf("%d%%", load/(threads/2)),
		"--hdd", fmt.Sprintf("%d", 1))
}

func stressFluidanimate(input benchInput) (*exec.Cmd, error) {
	return parsec(
		"-a", "run", "-p", "fluidanimate",
		"-n", fmt.Sprintf("%d", int(math.Pow(2, math.Logb(float64(input.threads))))),
		"-i", "native")
}

func stressFerret(threads int) (*exec.Cmd, error) {
	return parsec(
		"-a", "run", "-p", "ferret",
		"-n", fmt.Sprintf("%d", threads),
		"-i", "native")
}

func stressBlackschole(threads int) (*exec.Cmd, error) {
	return parsec(
		"-a", "run", "-p", "blackscholes",
		"-i", "native",
		"-n", fmt.Sprintf("%d", threads),
	)
}

func stressStreamCluster(threads int) (*exec.Cmd, error) {
	return parsec(
		"-a", "run", "-p", "streamcluster",
		"-i", "native",
		"-n", fmt.Sprintf("%d", threads),
	)
}

func stressSwaptions(threads int) (*exec.Cmd, error) {
	return parsec(
		"-a", "run", "-p", "swaptions",
		"-i", "native",
		"-n", fmt.Sprintf("%d", threads),
	)
}

func stressVips(threads int) (*exec.Cmd, error) {
	return parsec(
		"-a", "run", "-p", "vips",
		"-i", "native",
		"-n", fmt.Sprintf("%d", threads),
	)
}

func stressNetStreamCluster(threads int) (*exec.Cmd, error) {
	return parsec(
		"-a", "run", "-p", "netstreamcluster",
		"-i", "native",
		"-n", fmt.Sprintf("%d", threads),
	)
}

func stressNetFerrret(threads int) (*exec.Cmd, error) {
	return parsec(
		"-a", "run", "-p", "netferret",
		"-i", "native",
		"-n", fmt.Sprintf("%d", 4),
		"-t", fmt.Sprintf("%d", threads),
	)
}
