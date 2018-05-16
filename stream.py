#!/usr/bin/python

import requests
import time
import csv
import operator
from collections import Counter


def getCtxStream():
    url = 'http://localhost:8080/quizContextStream'

    resp = requests.get(url=url)
    data = resp.json()
    words = data['words']


    if words is not None:
        reader = csv.reader(open('ctx.csv'))
        freq = {}
        for row in reader:
            key = row[0]
            freq[key] = int(row[1])

        counts = Counter(words)
        for key in counts.keys():
            if key in freq.keys():
                freq[key] = int(freq[key]) + int(counts[key])
            else:
                freq[key] = int(counts[key])

        freq = sorted(freq.items(), key=operator.itemgetter(1), reverse=True)

        with open('ctx.csv', 'w') as csv_file:
            writer = csv.writer(csv_file)
            for key, value in freq:
                writer.writerow([key, value])

        


def main():
    while True:
        time.sleep(10)
        getCtxStream()


main()