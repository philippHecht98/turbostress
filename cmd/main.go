package main

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"reflect"
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
			var function_name = strings.Split(runtime.FuncForPC(reflect.ValueOf(stressFn).Pointer()).Name(), ".")[1]

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
			stress.Process.Kill()
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

func storageStress(input benchInput, conn net.Conn) error {
	return stress(input, "storage", conn, func(_, threads int) (*exec.Cmd, error) {
		return stressNGStorage(threads)
	})
}

func webserverStress(input benchInput, conn net.Conn) error {
	return stress(input, "webserver", conn, func(_, threads int) (*exec.Cmd, error) {
		return stressNGStorage(threads)
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

	input.initialLoad = 0
	if input.vm {
		err = vmStress(input, conn)
		if err != nil {
			return err
		}
	}

	err = storageStress(input, conn)
	if err != nil {
		return err
	}

	input.initialLoad = 0
	err = webserverStress(input, conn)
	if err != nil {
		return err
	}

	if input.maximize {
		err = maximizeStress(input, conn)
		if err != nil {
			return err
		}
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
	return stressNG("--vm", fmt.Sprintf("%d", threads), "--vm-bytes", fmt.Sprintf("%d%%", load))
}

func stressNGMAximize(threads int) (*exec.Cmd, error) {
	return stressNG("--cpu", fmt.Sprintf("%d", threads), "--vm", fmt.Sprintf("%d", threads), "--maximize")
}

func stressNGStorage(threads int) (*exec.Cmd, error) {
	return stressNG("--hdd", fmt.Sprintf("%d", threads))
}

func stressNGWebserver(load, threads int) (*exec.Cmd, error) {
	return stressNG(
		"--cpu", fmt.Sprintf("%d", threads),
		"--cpu-load", fmt.Sprintf("%d", load),
		"--vm", fmt.Sprintf("%d", threads),
		"--vm-bytes", fmt.Sprintf("%d", load),
		"--hdd", fmt.Sprintf("%d", threads))
}
