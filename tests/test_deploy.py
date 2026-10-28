"""Zero-setup deploy command builder tests."""

from rdp_manager.deploy import setup_command, receiver_script


def test_setup_command_contains_both_scripts_and_install():
    cmd = setup_command("192.0.2.5", "tarun", None)
    assert "scp" in cmd and "ssh" in cmd
    assert "tarun@192.0.2.5" in cmd
    assert "camera-receiver.sh" in cmd and "mic-receiver.sh" in cmd
    assert "--install" in cmd


def test_setup_command_no_username():
    cmd = setup_command("host.example", None, None)
    assert "host.example" in cmd
    assert "@" not in cmd.split("&&")[0].split("-o ConnectTimeout=8")[-1]


def test_receiver_scripts_exist():
    for name in ("camera-receiver.sh", "mic-receiver.sh"):
        path = receiver_script(name)
        assert path.endswith(name)
        with open(path) as f:
            assert f.read().strip()
