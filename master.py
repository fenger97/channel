from locust import User, task, events, constant

# Locust Master 配置文件
# 使用 Boomer 时，Locust 仅作为 Master 展示 UI 和聚合数据

class MyUser(User):
    wait_time = constant(1)
    
    @task
    def my_task(self):
        pass

@events.init.add_listener
def on_locust_init(environment, **kwargs):
    print("Locust Master initialized. Waiting for Boomer workers...")
