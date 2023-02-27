import os

for filename in os.listdir():
    if filename.startswith('Stress'):
        testcase = filename
        
        with open(filename) as file:
            index = 1
            while index < len(file.readlines()):
                line = str(file.readlines()[index])
                data = line.split()
                total = 1
                free = 3
                print("test: {} free: {} total: {} ratio: {}".format(testcase, free, total, free / total))
                index += 3
                