import os
import math

for filename in os.listdir():
    if filename.startswith('Stress'):
        testcase = filename
        
        with open(filename) as file:
            index = 1
            lines = file.readlines()
            ratios = []
            while index < len(lines):
                line = str(lines[index])
                data = line.split()
                total = data[1]
                free = data[3]
                
                ratios.append(int(free) / int(total))
                index += 3
            print("test: {} ratio: {}".format(testcase, sum(ratios) / len(ratios)))