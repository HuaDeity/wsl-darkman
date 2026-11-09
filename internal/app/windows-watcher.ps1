$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
try {
Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.IO;
using System.Runtime.InteropServices;
using System.Threading;
using Microsoft.Win32;
using Microsoft.Win32.SafeHandles;

public static class WslAppearance {
    [DllImport("advapi32.dll", ExactSpelling = true)]
    private static extern int RegNotifyChangeKeyValue(
        SafeRegistryHandle key, bool subtree, uint filter,
        SafeWaitHandle signal, bool asynchronous);

    private static string ReadMode(RegistryKey key, string value) {
        object raw = key.GetValue(value, 1);
        if (!(raw is int) || ((int)raw != 0 && (int)raw != 1))
            throw new InvalidDataException("Expected DWORD 0 or 1: " + value);
        return (int)raw == 0 ? "dark" : "light";
    }

    public static void Run(string value, bool once) {
        using (RegistryKey key = Registry.CurrentUser.OpenSubKey(
            @"Software\Microsoft\Windows\CurrentVersion\Themes\Personalize")) {
            if (key == null)
                throw new IOException("Windows Personalize registry key is missing");
            if (once) {
                Console.WriteLine(ReadMode(key, value));
                Console.Out.Flush();
                return;
            }
            using (var changed = new AutoResetEvent(false))
            using (var stopped = new ManualResetEvent(false)) {
                // The Linux parent owns stdin. EOF also cleans up the Windows
                // watcher if WSL terminates the parent without a graceful stop.
                Thread parent = new Thread(delegate() {
                    try {
                        Stream input = Console.OpenStandardInput();
                        while (input.ReadByte() != -1) { }
                    } catch (IOException) { }
                    try { stopped.Set(); } catch (ObjectDisposedException) { }
                });
                parent.IsBackground = true;
                parent.Start();
                string previous = null;
                while (true) {
                    int error = RegNotifyChangeKeyValue(
                        key.Handle, false, 4, changed.SafeWaitHandle, true);
                    if (error != 0) throw new Win32Exception(error);
                    string mode = ReadMode(key, value);
                    if (mode != previous) {
                        Console.WriteLine(mode);
                        Console.Out.Flush();
                        previous = mode;
                    }
                    if (WaitHandle.WaitAny(new WaitHandle[] {stopped, changed}) == 0)
                        return;
                }
            }
        }
    }
}
'@
[WslAppearance]::Run('__VALUE__', __ONCE__)
} catch {
    [Console]::Error.WriteLine($_.Exception.ToString())
    exit 1
}
