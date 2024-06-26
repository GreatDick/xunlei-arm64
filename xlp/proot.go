package xlp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
)

const _RUN_WITH_CHROOT = "RUN_WITH_CHROOT"

func NewRoot(root string) *Proot {
	return &Proot{Root: root, e: Env{}.init()}
}

type Proot struct {
	Root string

	e *Env
	s []string
	o []string
}

func (p *Proot) Bind(sources ...string) *Proot {
	p.s = append(p.s, sources...)
	return p
}

func (p *Proot) BindOptional(sources ...string) *Proot {
	p.o = append(p.o, sources...)
	return p
}

func (p *Proot) SetEnv(k, v string) *Proot {
	if p.e == nil {
		p.e = Env{}.init()
	}
	p.e.Set(k, v)
	return p
}

func (p *Proot) AppendEnv(envirions ...string) *Proot {
	if p.e == nil {
		p.e = Env{}.init()
	}
	p.e.Append(envirions...)
	return p
}

func (p *Proot) AppendOSEnv(extraEnvs ...string) *Proot {
	return p.AppendEnv(os.Environ()...).AppendEnv(extraEnvs...)
}

func (p *Proot) Environ() []string {
	if p.e != nil {
		return p.e.Environ()
	}
	return nil
}

func (p *Proot) Run(ctx context.Context, args ...string) (err error) {
	slog.Info("proot init", "root", p.Root)

	if p.Root == "" || p.Root == "/" {
		slog.Error("cannot run in /")
		return
	}

	var root string
	if root, err = filepath.Abs(p.Root); err != nil {
		return
	}

	var executable string
	if executable, err = os.Executable(); err != nil {
		return
	}

	var endpoints []string
	defer func() {
		for _, endpoint := range endpoints {
			if err := sysUnmount(endpoint); err == nil {
				slog.Debug("unmounted", "endpoint", endpoint)
				// } else {
				// 	slog.Warn("unmount failed", "endpoint", endpoint, "err", err)
			}
		}
	}()

	bind := func(optional bool, sources ...string) (err error) {
		for _, source := range sources {
			if source == "" {
				return errors.New("source is empty")
			}

			endpoint := filepath.Join(root, source)
			if err = mount(source, endpoint); err != nil {
				if !optional {
					return fmt.Errorf("mount %s to %s failed: %w", source, endpoint, err)
				} else {
					slog.Warn("bind", "src", source, "err", err)
					err = nil
					continue
				}
			}

			endpoints = append(endpoints, endpoint)
			slog.Debug("mounted", "source", source, "endpoint", endpoint)
		}
		return
	}

	if err = bind(false, p.s...); err != nil {
		return
	}

	if err = bind(true, p.o...); err != nil {
		return
	}

	if err = bind(true, executable); err != nil {
		return
	}

	// for _, ep := range endpoints {
	// 	fmt.Println(ep)
	// 	c := exec.CommandContext(ctx, "ls", "-lAhi", ep)
	// 	c.Stdout = os.Stdout
	// 	c.Stderr = os.Stderr
	// 	c.Run()
	// }

	c := exec.CommandContext(ctx, executable, args...)
	c.Stderr = os.Stderr
	c.Stdout = os.Stdout
	c.Env = p.SetEnv(_RUN_WITH_CHROOT, root).Environ()

	setupProcAttr(c, 0, 0)

	slog.Info("fork", "root", root, "command", c)
	if err = c.Start(); err != nil {
		return
	}
	slog.Info("fork", "pid", c.Process.Pid)

	return c.Wait()
}

func mount(source, endpoint string) (err error) {
	var srcStat os.FileInfo
	if srcStat, err = os.Stat(source); err != nil {
		return
	}

	var dstStat os.FileInfo
	var dstNotExist bool
	if dstStat, err = os.Stat(endpoint); err != nil {
		if dstNotExist = os.IsNotExist(err); !dstNotExist {
			return
		}
		err = nil
	}

	if srcStat.IsDir() {
		if dstNotExist {
			err = os.MkdirAll(endpoint, srcStat.Mode())
		} else if !dstStat.IsDir() {
			err = fmt.Errorf("endpoint %s is not a directory", endpoint)
		}
	} else {
		if !dstNotExist && !srcStat.Mode().IsRegular() {
			err = fmt.Errorf("endpoint %s is not a regular file", endpoint)
		}
	}

	if err == nil {
		err = sysMount(source, endpoint)
	}
	return
}
