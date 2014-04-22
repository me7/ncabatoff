package main

import (
	"code.google.com/p/ncabatoff/imglib"
	"code.google.com/p/ncabatoff/imgseq"
	"code.google.com/p/ncabatoff/motion"
	"code.google.com/p/ncabatoff/vlib"
	"flag"
	"fmt"
	"github.com/golang/glog"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"syscall"
)

var (
	// The initial width and height of the window.
	flagWidth, flagHeight int

	// If set, the image window will automatically resize to the first image
	// that it displays.
	flagAutoResize bool

	// The amount to increment panning when using h,j,k,l
	flagStepIncrement int

	// Whether to run a CPU profile.
	flagProfile string

	// When set, bv will print all keybindings and exit.
	flagKeybindings bool

	flagDeltaThresh int
)

func init() {
	// Set GOMAXPROCS, since bv can benefit greatly from parallelism.
	runtime.GOMAXPROCS(runtime.NumCPU())

	// Set all of the flags.
	flag.IntVar(&flagWidth, "width", 480,
		"The initial width of the window.")
	flag.IntVar(&flagHeight, "height", 640,
		"The initial height of the window.")
	flag.BoolVar(&flagAutoResize, "auto-resize", false,
		"If set, window will resize to size of first image.")
	flag.IntVar(&flagStepIncrement, "increment", 20,
		"The increment (in pixels) used to pan the image.")
	flag.StringVar(&flagProfile, "profile", "",
		"If set, a CPU profile will be saved to the file name provided.")
	flag.BoolVar(&flagKeybindings, "keybindings", false,
		"If set, bv will output a list all keybindings.")

	// flag.IntVar(&flagStartFrame, "start", 0,
	//		"If set, bv will start at this frame")

	flag.IntVar(&flagDeltaThresh, "deltaThresh", 32*69,
		"The delta filter threshold.")
	flag.Usage = usage
	flag.Parse()

	// Do some error checking on the flag values... naughty!
	if flagWidth == 0 || flagHeight == 0 {
		glog.Fatal("The width and height must be non-zero values.")
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s [flags] image-file [image-file ...]\n",
		filepath.Base(os.Args[0]))
	flag.PrintDefaults()
	os.Exit(1)
}

func main() {
	defer func() {
		glog.Flush()
	}()
	runtime.GOMAXPROCS(runtime.NumCPU())

	// If we just need the keybindings, print them and be done.
	if flagKeybindings {
		for _, keyb := range vlib.Keybinds {
			fmt.Printf("%-10s %s\n", keyb.Key, keyb.Desc)
		}
		fmt.Printf("%-10s %s\n", "mouse",
			"Left mouse button will pan the image.")
		os.Exit(0)
	}

	// Run the CPU profile if we're instructed to.
	if len(flagProfile) > 0 {
		f, err := os.Create(flagProfile)
		if err != nil {
			glog.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	// Whoops!
	if flag.NArg() == 0 {
		fmt.Fprint(os.Stderr, "\n")
		glog.Errorf("No images specified.\n\n")
		usage()
	}

	fi, err := os.Stat(flag.Arg(0))
	if err != nil {
		glog.Fatalf("unable to stat input '%s': %v", flag.Arg(0), err)
	}
	if fi.IsDir() {
		viewDir(flag.Arg(0))
	} else if fi.Mode().IsRegular() {
		viewFileMmap(flag.Arg(0))
	} else {
		viewDevice(flag.Arg(0))
	}
}

func viewDir(path string) {
	dl := imgseq.GetDirList(path)
	imgs := make([][]imgseq.Img, 0, 100)
	imagechan := make(chan imgseq.Img, 100)
	go imgseq.LoadImages(dl, imagechan)
	filtchan := make(chan []imgseq.Img, 100)
	go filterInactive(imagechan, filtchan)

	for img := range filtchan {
		imgs = append(imgs, img)
	}

	glog.Infof("starting viewer for %d images", len(imgs))
	vlib.ViewImages(func(i int) (int, []image.Image) {
		lg("got %d", i)
		if i >= len(imgs) {
			i = 0
		}
		if i < 0 {
			i = len(imgs) - 1
		}
		return i, []image.Image{imgs[i][0].Image, imgs[i][1].Image}
	})
}

func viewFileMmap(path string) {
	rect := image.Rect(0, 0, flagWidth, flagHeight)
	imgsize := 2 * (rect.Size().X * rect.Size().Y)
	var buffer []byte
	var fileSize int64
	if fl, err := os.Open(path); err != nil {
		glog.Fatalf("unable to open %s: %v", path, err)
	} else {
		if fi, err := fl.Stat(); err != nil {
			glog.Fatalf("unable to stat %s: %v", path, err)
		} else {
			fileSize = fi.Size()
		}

		if buffer, err = syscall.Mmap(int(fl.Fd()), 0, int(fileSize),
			syscall.PROT_READ, syscall.MAP_SHARED); err != nil {
			glog.Fatalf("unable to mmap %s: %v", path, err)
		}
	}
	numimgs := int(fileSize) / imgsize

	defer func(buf []byte) {
		err := syscall.Munmap(buf)
		lp("unmap err=%v", err)
	}(buffer)

	vlib.ViewImages(func(i int) (int, []image.Image) {
		lg("got %d", i)
		if i >= numimgs {
			i = 0
		}
		if i < 0 {
			i = numimgs - 1
		}
		pix := buffer[imgsize*i : imgsize*i+imgsize]
		yuyv := imglib.YUYV{Pix: pix, Stride: rect.Dx() * 2, Rect: rect}
		return i, []image.Image{imglib.StdImage{&yuyv}.GetRGBA()}
	})
}

func getFileAndSize(path string) (*os.File, int64) {
	if fl, err := os.Open(path); err != nil {
		glog.Fatalf("unable to open %s: %v", path, err)
	} else {
		if fi, err := fl.Stat(); err != nil {
			glog.Fatalf("unable to stat %s: %v", path, err)
		} else {
			return fl, fi.Size()
		}
	}
	return nil, 0
}

func viewDevice(path string) {
	rect := image.Rect(0, 0, flagWidth, flagHeight)
	imgsize := 2 * (rect.Size().X * rect.Size().Y)
	fl, _ := getFileAndSize(path)
	// lp("devsize=%d", devSize)
	// numimgs := int(devSize) / imgsize

	lasti := -1
	yuyv := imglib.NewYUYV(rect)
	vlib.ViewImages(func(i int) (int, []image.Image) {
		lg("got %d", i)
		if i != lasti+1 {
			if newpos, err := fl.Seek(int64(i*imgsize), os.SEEK_SET); err != nil {
				glog.Fatalf("seek failure: lastpos=%d, endpos=%d, err=%v", lasti*imgsize, newpos, err)
			}
		}

		if _, err := fl.Read(yuyv.Pix); err != nil {
			if err != io.EOF {
				glog.Fatalf("error reading: %v", err)
			} else {
				lasti = i
			}
		}

		return i, []image.Image{imglib.StdImage{yuyv}.GetRGBA()}
	})
}

func filterInactive(imgin chan imgseq.Img, imgout chan []imgseq.Img) {
	trk := motion.NewTracker()
	for img := range imgin {
		glog.V(1).Infof("filtering img %v with %d bytes", img.ImgInfo)
		if rs := trk.GetRects(img, flagDeltaThresh); len(rs) > 0 {
			sort.Sort(motion.RectAreaSlice(rs))
			newimg := img
			newimg.Image = getRectImage(img, rs)
			imgout <- []imgseq.Img{img, newimg}
		}
	}
	close(imgout)
}

func getRectImage(img imgseq.Img, rects []image.Rectangle) image.Image {
    irect := img.Bounds()
    out := image.NewRGBA(irect)
    for _, r := range rects {
        // sz := r.Size()
        // ar := sz.X * sz.Y
        // if ar > 15 && ar < 250 {
            draw.Draw(out, r.Inset(-1), &image.Uniform{color.White}, image.ZP, draw.Src)
            draw.Draw(out, r, img.Image, r.Min, draw.Src)
        // }
    }
    return out
}