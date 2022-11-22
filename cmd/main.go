package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
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
		loadDurationBeforeMeasures: time.Duration(5 * time.Second),
		threads:                    runtime.NumCPU(),
		metrics:                    powerMetrics,
		repeat:                     10,
		durationBetweenMeasures:    time.Duration(1 * time.Second),
		method:                     "all",
		cpuInfo:                    true,
		ipsec:                      true,
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

func requestTesting(connection net.Conn, arguments benchInput) error {
	logrus.Info("Requested to start a Test")

	msg := "startTestReq\n"
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
	logrus.Info("Requested to start a Test")

	msg := "startTestReq\n"
	connection.Write([]byte(msg))
}

func stress(input benchInput, name string, conn net.Conn, stressFn func(load int, threads int) (*exec.Cmd, error)) error {
	var load = input.initialLoad

	for {
		logrus.Infof("load_duration_before_measure: %ds, load: %d, threads: %d", int(input.loadDurationBeforeMeasures.Seconds()), load, input.threads)
		// initialize TCP Connection to Bare-Metal
		err := requestTesting(conn, input)
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

		// increase load of stress test
		if load == 100 {
			return nil
		}

		load += input.loadStep
		if load > 100 {
			load = 100
		}
	}
}

func cpuStress(input benchInput, conn net.Conn) error {
	return stress(input, "CPUStress", conn, func(load int, threads int) (*exec.Cmd, error) {
		return stressNGCPUStress(load, threads, input.method)
	})
}

func vmStress(input benchInput, conn net.Conn) error {
	return stress(input, "VMStress", conn, func(_, threads int) (*exec.Cmd, error) {
		return stressNGVMStress(threads)
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
		input.initialLoad = 100
		err = ipsecStress(input, conn)
		if err != nil {
			return err
		}
	}

	if input.vm {
		err = vmStress(input, conn)
		if err != nil {
			return err
		}
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

func stressNGVMStress(threads int) (*exec.Cmd, error) {
	return stressNG("--vm", fmt.Sprintf("%d", threads))
}

func stressNGMAximize(threads int) (*exec.Cmd, error) {
	return stressNG("--cpu", fmt.Sprintf("%d", threads), "--vm", fmt.Sprintf("%d", threads), "--maximize")
}

func turboStat(stats []string, durationBetweenMeasures time.Duration) ([]float64, error) {
	cmd := exec.Command("turbostat", "-q", "-c", "package", "--num_iterations", "1", "--interval", fmt.Sprintf("%02f", durationBetweenMeasures.Seconds()), "--show", strings.Join(stats, ","))
	logrus.Info(cmd.Args)
	stdout := bytes.NewBuffer(nil)
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	err := cmd.Start()
	if err != nil {
		return nil, err
	}
	err = cmd.Wait()
	if err != nil {
		return nil, err
	}
	output := stdout.String()
	lines := strings.Split(output, "\n")
	results := make(map[string]float64)
	if len(lines) >= 2 {
		names := strings.Split(lines[0], "\t")
		values := strings.Split(lines[1], "\t")
		for index, value := range values {
			f, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return nil, err
			}
			results[names[index]] = f
		}
		var ret []float64
		for _, key := range stats {
			ret = append(ret, results[key])
		}
		return ret, nil
	}
	return nil, fmt.Errorf("could not parse turbostat output: %s", output)
}
