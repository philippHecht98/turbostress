import subprocess
import psutil
import math

def get_command(prog, threads):
    return ['../parsec/parsec-3.0/bin/parsecmgmt', '-a',  'run', '-p', prog, '-i', 'simlarge', '-n', threads]
    
    
def execute_command(prog):
    pid = subprocess.Popen(
        prog,
        stdout=subprocess.PIPE,
        stderr=None,
        shell=False
    )
    return pid
    
cpu_size = psutil.cpu_count()


programs = ['fluidanimate', 'ferret', 'blackscholes', 'streamcluster', 'swaptions', 'vips', 'netstreamcluster', 'netferret']

for program in programs:
    threads = cpu_size
    if program == 'fluidanimate':
        threads = int(math.log2(cpu_size))
    execute_command(get_command(program, threads))
