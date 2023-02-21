import subprocess
import psutil
import math
import socket

def get_command(prog, threads):
    return ['../parsec/parsec-3.0/bin/parsecmgmt', '-a',  'run', '-p', prog, '-i', 'simlarge', '-n', str(threads)]
    
    
def execute_command(prog):
    print(prog)
    pid = subprocess.Popen(
        prog,
        stdout=subprocess.PIPE,
        stderr=None,
        shell=False
    )
    return pid
    
cpu_size = psutil.cpu_count()


programs = ['fluidanimate', 'ferret', 'blackscholes', 'streamcluster', 'swaptions', 'vips', 'netstreamcluster', 'netferret']


#host_address = tuple('192.168.122.1', 4444)

#sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)

#sock.bind(('0.0.0.0', '4445'))

for program in programs:
    threads = cpu_size
    if program == 'fluidanimate':
        threads = 2 ** int(math.log(cpu_size, 2))
            
    pid = execute_command(get_command(program, threads))
    pid.wait()
