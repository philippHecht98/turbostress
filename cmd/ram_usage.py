import os

for filename in os.listdir():
    if filename.startswith('Stress'):
        testcase = filename
        
        with open(filename) as file:
            index = 1
            lines = file.readlines()
            while index < len(lines):
                line = str(lines[index])
                data = line.split()
                print(data)
                total = data[1]
                free = data[3]
                print("test: {} free: {} total: {} ratio: {}".format(testcase, free, total, int(free) / int(total)))
                index += 3
                