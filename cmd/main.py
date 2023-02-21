import subprocess
import psutil
import math
import socket
import time

def get_command(prog, threads):
    return ['../parsec/parsec-3.0/bin/parsecmgmt', '-a',  'run', '-p', prog, '-i', 'simlarge', '-n', str(threads)]
    
    
def execute_command(prog):
    print(prog)
    pid = subprocess.Popen(
        prog,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        shell=False
    )
    return pid
    
cpu_size = psutil.cpu_count()


programs = ['fluidanimate', 'ferret', 'blackscholes', 'streamcluster', 'swaptions', 'vips', 'netstreamcluster', 'netferret']


host_address = tuple(['192.168.122.1', 4444])

sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)

sock.bind(('0.0.0.0', 4445))
sock.connect(host_address)

repitition = 1

for program in programs:
    
    threads = cpu_size
    if program == 'fluidanimate':
        threads = 2 ** int(math.log(cpu_size, 2))
    
    
    for i in range(repitition):
        sock.send('{}/{}/{}\n'.format(program, str(100), str(i)).encode('utf-8'))
        pid = execute_command(get_command(program, threads))
        pid.wait()
        sock.send('fin\n')
        time.sleep(10)

sock.send('finished recording\n')