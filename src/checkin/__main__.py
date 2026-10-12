"""``python -m checkin`` 入口（launchd 定时任务就用这种方式拉起）。"""
from checkin.cli import main

if __name__ == "__main__":
    main()