//go:build privileged

package utils

import (
	"context"
	"net"
	"runtime"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/vishvananda/netlink"

	"github.com/AliyunContainerService/terway/pkg/tc"
	terwayTypes "github.com/AliyunContainerService/terway/types"
)

func TestCNI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Utils Suite")
}

var _ = Describe("Utils", func() {
	Describe("EnsureVlanUntagger", func() {
		It("should ensure vlan untagger successfully", func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()

			var err error
			hostNS, err := testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			containerNS, err := testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			err = hostNS.Set()
			Expect(err).NotTo(HaveOccurred())

			defer func() {
				err := containerNS.Close()
				Expect(err).NotTo(HaveOccurred())

				err = testutils.UnmountNS(containerNS)
				Expect(err).NotTo(HaveOccurred())

				err = hostNS.Close()
				Expect(err).NotTo(HaveOccurred())

				err = testutils.UnmountNS(hostNS)
				Expect(err).NotTo(HaveOccurred())
			}()

			err = netlink.LinkAdd(&netlink.Dummy{
				LinkAttrs: netlink.LinkAttrs{Name: "eni"},
			})
			Expect(err).NotTo(HaveOccurred())

			eni, err := netlink.LinkByName("eni")
			Expect(err).NotTo(HaveOccurred())

			err = EnsureVlanUntagger(context.Background(), eni)
			Expect(err).NotTo(HaveOccurred())

			// should not have error
			err = EnsureVlanUntagger(context.Background(), eni)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("Link operations", func() {
		var hostNS ns.NetNS
		const nicName = "test-nic"

		BeforeEach(func() {
			var err error
			hostNS, err = testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				return netlink.LinkAdd(&netlink.Dummy{
					LinkAttrs: netlink.LinkAttrs{Name: nicName},
				})
			})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			_ = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				link, err := netlink.LinkByName(nicName)
				if err == nil {
					err = netlink.LinkDel(link)
					Expect(err).NotTo(HaveOccurred())
				}
				return nil
			})
			Expect(hostNS.Close()).To(Succeed())
			Expect(testutils.UnmountNS(hostNS)).To(Succeed())
		})

		Describe("EnsureLinkUp", func() {
			It("should set link up when it is down", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					// Set link down first
					err = netlink.LinkSetDown(link)
					Expect(err).NotTo(HaveOccurred())

					changed, err = EnsureLinkUp(context.Background(), link)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeTrue())

					// Verify link is up
					link, err = netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())
					Expect(link.Attrs().Flags & net.FlagUp).NotTo(Equal(0))

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should not change when link is already up", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					// Ensure link is up
					err = netlink.LinkSetUp(link)
					Expect(err).NotTo(HaveOccurred())

					// Read again
					link, err = netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					changed, err = EnsureLinkUp(context.Background(), link)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeFalse())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("EnsureLinkMTU", func() {
			It("should set MTU when different", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					originalMTU := link.Attrs().MTU
					newMTU := 1500
					if originalMTU == newMTU {
						newMTU = 1400
					}

					changed, err = EnsureLinkMTU(context.Background(), link, newMTU)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeTrue())

					// Verify MTU is set
					link, err = netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())
					Expect(link.Attrs().MTU).To(Equal(newMTU))

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should not change when MTU is already set", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					currentMTU := link.Attrs().MTU

					changed, err = EnsureLinkMTU(context.Background(), link, currentMTU)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeFalse())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("EnsureLinkName", func() {
			It("should rename link when name is different", func() {
				var err error
				var changed bool
				newName := "renamed-nic"

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					changed, err = EnsureLinkName(context.Background(), link, newName)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeTrue())

					// Verify link is renamed
					_, err = netlink.LinkByName(newName)
					Expect(err).NotTo(HaveOccurred())

					// Verify old name doesn't exist
					_, err = netlink.LinkByName(nicName)
					Expect(err).To(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should not change when name is already correct", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					changed, err = EnsureLinkName(context.Background(), link, nicName)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeFalse())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("EnsureLinkMAC", func() {
			It("should set MAC address when different", func() {
				var err error
				newMAC := "00:11:22:33:44:55"

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					err = EnsureLinkMAC(context.Background(), link, newMAC)
					Expect(err).NotTo(HaveOccurred())

					// Verify MAC is set
					link, err = netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())
					Expect(link.Attrs().HardwareAddr.String()).To(Equal(newMAC))

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should not change when MAC is already correct", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					currentMAC := link.Attrs().HardwareAddr.String()

					err = EnsureLinkMAC(context.Background(), link, currentMAC)
					Expect(err).NotTo(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should return error for invalid MAC address", func() {
				var err error
				invalidMAC := "invalid-mac"

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					err = EnsureLinkMAC(context.Background(), link, invalidMAC)
					Expect(err).To(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("DelLinkByName", func() {
			It("should delete link by name", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					// Verify link exists
					_, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					err = DelLinkByName(context.Background(), nicName)
					Expect(err).NotTo(HaveOccurred())

					// Verify link is deleted
					_, err = netlink.LinkByName(nicName)
					Expect(err).To(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should not return error when link doesn't exist", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					err = DelLinkByName(context.Background(), "non-exist")
					Expect(err).NotTo(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("EnsureAddr", func() {
			It("should add address when none exists", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					addr := &netlink.Addr{
						IPNet: &net.IPNet{
							IP:   net.ParseIP("192.168.1.100"),
							Mask: net.CIDRMask(24, 32),
						},
						Scope: int(netlink.SCOPE_UNIVERSE),
					}

					changed, err = EnsureAddr(context.Background(), link, addr)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeTrue())

					// Verify address is added
					addrList, err := netlink.AddrList(link, netlink.FAMILY_V4)
					Expect(err).NotTo(HaveOccurred())

					found := false
					for _, existingAddr := range addrList {
						if existingAddr.IPNet.String() == addr.IPNet.String() {
							found = true
							break
						}
					}
					Expect(found).To(BeTrue())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should replace existing address with different one", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					// Add initial address
					initialAddr := &netlink.Addr{
						IPNet: &net.IPNet{
							IP:   net.ParseIP("192.168.1.100"),
							Mask: net.CIDRMask(24, 32),
						},
						Scope: int(netlink.SCOPE_UNIVERSE),
					}

					err = netlink.AddrAdd(link, initialAddr)
					Expect(err).NotTo(HaveOccurred())

					// Replace with new address
					newAddr := &netlink.Addr{
						IPNet: &net.IPNet{
							IP:   net.ParseIP("192.168.1.200"),
							Mask: net.CIDRMask(24, 32),
						},
						Scope: int(netlink.SCOPE_UNIVERSE),
					}

					changed, err = EnsureAddr(context.Background(), link, newAddr)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeTrue())

					// Verify old address is removed and new one is added
					addrList, err := netlink.AddrList(link, netlink.FAMILY_V4)
					Expect(err).NotTo(HaveOccurred())

					oldFound := false
					newFound := false
					for _, existingAddr := range addrList {
						if existingAddr.IPNet.String() == initialAddr.IPNet.String() {
							oldFound = true
						}
						if existingAddr.IPNet.String() == newAddr.IPNet.String() {
							newFound = true
						}
					}
					Expect(oldFound).To(BeFalse())
					Expect(newFound).To(BeTrue())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should not change when address already exists", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					addr := &netlink.Addr{
						IPNet: &net.IPNet{
							IP:   net.ParseIP("192.168.1.100"),
							Mask: net.CIDRMask(24, 32),
						},
						Scope: int(netlink.SCOPE_UNIVERSE),
					}

					// Add address first
					err = netlink.AddrAdd(link, addr)
					Expect(err).NotTo(HaveOccurred())

					// Try to ensure same address
					changed, err = EnsureAddr(context.Background(), link, addr)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeFalse())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})
	})

	Describe("EnsureHostNsConfig", func() {
		It("should configure host namespace for IPv4", func() {
			err := EnsureHostNsConfig(true, false)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should configure host namespace for IPv6", func() {
			err := EnsureHostNsConfig(false, true)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should configure host namespace for both IPv4 and IPv6", func() {
			err := EnsureHostNsConfig(true, true)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("IPNet utilities", func() {
		Describe("NewIPNetWithMaxMask", func() {
			It("should create IPv4 net with /32 mask", func() {
				ipNet := &net.IPNet{
					IP:   net.ParseIP("192.168.1.1"),
					Mask: net.CIDRMask(24, 32),
				}

				result := NewIPNetWithMaxMask(ipNet)
				Expect(result.IP.String()).To(Equal("192.168.1.1"))
				Expect(result.Mask.String()).To(Equal("ffffffff"))
			})

			It("should create IPv6 net with /128 mask", func() {
				ipNet := &net.IPNet{
					IP:   net.ParseIP("fd00::1"),
					Mask: net.CIDRMask(64, 128),
				}

				result := NewIPNetWithMaxMask(ipNet)
				Expect(result.IP.String()).To(Equal("fd00::1"))
				Expect(result.Mask.String()).To(Equal("ffffffffffffffffffffffffffffffff"))
			})
		})

		Describe("NewIPNet1", func() {
			It("should create addr list from IPv4 only", func() {
				ipNetSet := &terwayTypes.IPNetSet{
					IPv4: &net.IPNet{
						IP:   net.ParseIP("192.168.1.1"),
						Mask: net.CIDRMask(24, 32),
					},
				}

				addrs := NewIPNet1(ipNetSet)
				Expect(len(addrs)).To(Equal(1))
				Expect(addrs[0].IPNet.String()).To(Equal("192.168.1.1/24"))
			})

			It("should create addr list from IPv6 only", func() {
				ipNetSet := &terwayTypes.IPNetSet{
					IPv6: &net.IPNet{
						IP:   net.ParseIP("fd00::1"),
						Mask: net.CIDRMask(64, 128),
					},
				}

				addrs := NewIPNet1(ipNetSet)
				Expect(len(addrs)).To(Equal(1))
				Expect(addrs[0].IPNet.String()).To(Equal("fd00::1/64"))
			})

			It("should create addr list from both IPv4 and IPv6", func() {
				ipNetSet := &terwayTypes.IPNetSet{
					IPv4: &net.IPNet{
						IP:   net.ParseIP("192.168.1.1"),
						Mask: net.CIDRMask(24, 32),
					},
					IPv6: &net.IPNet{
						IP:   net.ParseIP("fd00::1"),
						Mask: net.CIDRMask(64, 128),
					},
				}

				addrs := NewIPNet1(ipNetSet)
				Expect(len(addrs)).To(Equal(2))
				Expect(addrs[0].IPNet.String()).To(Equal("192.168.1.1/24"))
				Expect(addrs[1].IPNet.String()).To(Equal("fd00::1/64"))
			})
		})

		Describe("NewIPNetToMaxMask", func() {
			It("should create addr list with max mask for IPv4", func() {
				ipNetSet := &terwayTypes.IPNetSet{
					IPv4: &net.IPNet{
						IP:   net.ParseIP("192.168.1.1"),
						Mask: net.CIDRMask(24, 32),
					},
				}

				addrs := NewIPNetToMaxMask(ipNetSet)
				Expect(len(addrs)).To(Equal(1))
				Expect(addrs[0].IPNet.String()).To(Equal("192.168.1.1/32"))
			})

			It("should create addr list with max mask for IPv6", func() {
				ipNetSet := &terwayTypes.IPNetSet{
					IPv6: &net.IPNet{
						IP:   net.ParseIP("fd00::1"),
						Mask: net.CIDRMask(64, 128),
					},
				}

				addrs := NewIPNetToMaxMask(ipNetSet)
				Expect(len(addrs)).To(Equal(1))
				Expect(addrs[0].IPNet.String()).To(Equal("fd00::1/128"))
			})

			It("should return nil for nil input", func() {
				addrs := NewIPNetToMaxMask(nil)
				Expect(addrs).To(BeNil())
			})
		})

		Describe("NewIPNet", func() {
			It("should create IPNetSet with max mask for IPv4", func() {
				ipNetSet := &terwayTypes.IPNetSet{
					IPv4: &net.IPNet{
						IP:   net.ParseIP("192.168.1.1"),
						Mask: net.CIDRMask(24, 32),
					},
				}

				result := NewIPNet(ipNetSet)
				Expect(result.IPv4.String()).To(Equal("192.168.1.1/32"))
				Expect(result.IPv6).To(BeNil())
			})

			It("should create IPNetSet with max mask for IPv6", func() {
				ipNetSet := &terwayTypes.IPNetSet{
					IPv6: &net.IPNet{
						IP:   net.ParseIP("fd00::1"),
						Mask: net.CIDRMask(64, 128),
					},
				}

				result := NewIPNet(ipNetSet)
				Expect(result.IPv6.String()).To(Equal("fd00::1/128"))
				Expect(result.IPv4).To(BeNil())
			})
		})
	})

	Describe("Route operations", func() {
		var hostNS ns.NetNS
		const nicName = "route-test-nic"

		BeforeEach(func() {
			var err error
			hostNS, err = testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				return netlink.LinkAdd(&netlink.Dummy{
					LinkAttrs: netlink.LinkAttrs{Name: nicName},
				})
			})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			_ = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				link, err := netlink.LinkByName(nicName)
				if err == nil {
					err = netlink.LinkDel(link)
					Expect(err).NotTo(HaveOccurred())
				}
				return nil
			})
			Expect(hostNS.Close()).To(Succeed())
			Expect(testutils.UnmountNS(hostNS)).To(Succeed())
		})

		Describe("FoundRoutes", func() {
			It("should return error for nil destination", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					route := &netlink.Route{
						Dst: nil,
					}

					_, err := FoundRoutes(route)
					Expect(err).To(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should find routes with IPv4 destination", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					// Set link up first
					err = netlink.LinkSetUp(link)
					Expect(err).NotTo(HaveOccurred())

					// Add IP address to the link, which will automatically create a route
					addr := &netlink.Addr{
						IPNet: &net.IPNet{
							IP:   net.ParseIP("192.168.1.224"),
							Mask: net.CIDRMask(24, 32),
						},
						Scope: int(netlink.SCOPE_UNIVERSE),
					}
					err = netlink.AddrAdd(link, addr)
					Expect(err).NotTo(HaveOccurred())

					// Find the automatically created route
					searchRoute := &netlink.Route{
						Dst: &net.IPNet{
							IP:   net.ParseIP("192.168.1.0"),
							Mask: net.CIDRMask(24, 32),
						},
					}

					routes, err := FoundRoutes(searchRoute)
					Expect(err).NotTo(HaveOccurred())
					Expect(len(routes)).To(BeNumerically(">", 0))

					// Verify found route matches the automatically created one
					found := false
					for _, route := range routes {
						if route.Dst.String() == "192.168.1.0/24" &&
							route.LinkIndex == link.Attrs().Index &&
							route.Scope == netlink.SCOPE_LINK {
							found = true
							break
						}
					}
					Expect(found).To(BeTrue())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should find routes with IPv6 destination", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					// Set link up first
					err = netlink.LinkSetUp(link)
					Expect(err).NotTo(HaveOccurred())

					// Add IPv6 address to the link, which will automatically create a route
					addr := &netlink.Addr{
						IPNet: &net.IPNet{
							IP:   net.ParseIP("fd00:100::224"),
							Mask: net.CIDRMask(64, 128),
						},
						Scope: int(netlink.SCOPE_UNIVERSE),
					}
					err = netlink.AddrAdd(link, addr)
					Expect(err).NotTo(HaveOccurred())

					// Find the automatically created route
					searchRoute := &netlink.Route{
						Dst: &net.IPNet{
							IP:   net.ParseIP("fd00:100::"),
							Mask: net.CIDRMask(64, 128),
						},
					}

					routes, err := FoundRoutes(searchRoute)
					Expect(err).NotTo(HaveOccurred())
					Expect(len(routes)).To(BeNumerically(">", 0))

					// Verify found route matches the automatically created one
					found := false
					for _, route := range routes {
						if route.Dst.String() == "fd00:100::/64" &&
							route.LinkIndex == link.Attrs().Index &&
							route.Scope == netlink.SCOPE_UNIVERSE {
							found = true
							break
						}
					}
					Expect(found).To(BeTrue())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should find routes with default gateway (0.0.0.0/0)", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					// Set link up first
					err = netlink.LinkSetUp(link)
					Expect(err).NotTo(HaveOccurred())

					// Add IP address to the link first
					addr := &netlink.Addr{
						IPNet: &net.IPNet{
							IP:   net.ParseIP("192.168.1.224"),
							Mask: net.CIDRMask(24, 32),
						},
						Scope: int(netlink.SCOPE_UNIVERSE),
					}
					err = netlink.AddrAdd(link, addr)
					Expect(err).NotTo(HaveOccurred())

					// Add a default route with Onlink flag to avoid gateway reachability check
					testRoute := &netlink.Route{
						Dst: &net.IPNet{
							IP:   net.ParseIP("0.0.0.0"),
							Mask: net.CIDRMask(0, 32),
						},
						Gw:        net.ParseIP("192.168.1.1"),
						LinkIndex: link.Attrs().Index,
						Flags:     int(netlink.FLAG_ONLINK),
					}

					err = netlink.RouteAdd(testRoute)
					Expect(err).NotTo(HaveOccurred())

					// Find the route with 0.0.0.0/0
					searchRoute := &netlink.Route{
						Dst: &net.IPNet{
							IP:   net.ParseIP("0.0.0.0"),
							Mask: net.CIDRMask(0, 32),
						},
					}

					routes, err := FoundRoutes(searchRoute)
					Expect(err).NotTo(HaveOccurred())
					Expect(len(routes)).To(BeNumerically(">", 0))

					// Verify found route matches
					found := false
					for _, route := range routes {
						if route.Dst == nil &&
							route.Gw.String() == testRoute.Gw.String() &&
							route.LinkIndex == testRoute.LinkIndex {
							found = true
							break
						}
					}
					Expect(found).To(BeTrue())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should find routes with default gateway (::/0)", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					// Set link up first
					err = netlink.LinkSetUp(link)
					Expect(err).NotTo(HaveOccurred())

					// Add IPv6 address to the link first
					addr := &netlink.Addr{
						IPNet: &net.IPNet{
							IP:   net.ParseIP("fd00:100::224"),
							Mask: net.CIDRMask(64, 128),
						},
						Scope: int(netlink.SCOPE_UNIVERSE),
					}
					err = netlink.AddrAdd(link, addr)
					Expect(err).NotTo(HaveOccurred())

					// Add a default IPv6 route with Onlink flag to avoid gateway reachability check
					testRoute := &netlink.Route{
						Dst: &net.IPNet{
							IP:   net.ParseIP("::"),
							Mask: net.CIDRMask(0, 128),
						},
						Gw:        net.ParseIP("fd00::1"),
						LinkIndex: link.Attrs().Index,
						Flags:     int(netlink.FLAG_ONLINK),
					}

					err = netlink.RouteAdd(testRoute)
					Expect(err).NotTo(HaveOccurred())

					// Find the route with ::/0
					searchRoute := &netlink.Route{
						Dst: &net.IPNet{
							IP:   net.ParseIP("::"),
							Mask: net.CIDRMask(0, 128),
						},
					}

					routes, err := FoundRoutes(searchRoute)
					Expect(err).NotTo(HaveOccurred())
					Expect(len(routes)).To(BeNumerically(">", 0))

					// Verify found route matches
					found := false
					for _, route := range routes {
						if route.Dst == nil &&
							route.Gw.String() == testRoute.Gw.String() &&
							route.LinkIndex == testRoute.LinkIndex {
							found = true
							break
						}
					}
					Expect(found).To(BeTrue())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should find routes with link index filter", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					// Set link up first
					err = netlink.LinkSetUp(link)
					Expect(err).NotTo(HaveOccurred())

					// Add IP address to the link, which will automatically create a route
					addr := &netlink.Addr{
						IPNet: &net.IPNet{
							IP:   net.ParseIP("192.168.200.224"),
							Mask: net.CIDRMask(24, 32),
						},
						Scope: int(netlink.SCOPE_UNIVERSE),
					}
					err = netlink.AddrAdd(link, addr)
					Expect(err).NotTo(HaveOccurred())

					// Find the route with link index filter
					searchRoute := &netlink.Route{
						Dst: &net.IPNet{
							IP:   net.ParseIP("192.168.200.0"),
							Mask: net.CIDRMask(24, 32),
						},
						LinkIndex: link.Attrs().Index,
					}

					routes, err := FoundRoutes(searchRoute)
					Expect(err).NotTo(HaveOccurred())
					Expect(len(routes)).To(BeNumerically(">", 0))

					// Verify found route matches the automatically created one
					found := false
					for _, route := range routes {
						if route.Dst.String() == "192.168.200.0/24" &&
							route.LinkIndex == link.Attrs().Index &&
							route.Scope == netlink.SCOPE_LINK {
							found = true
							break
						}
					}
					Expect(found).To(BeTrue())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should find routes with gateway filter", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					// Set link up first
					err = netlink.LinkSetUp(link)
					Expect(err).NotTo(HaveOccurred())

					// Add IP address to the link first
					addr := &netlink.Addr{
						IPNet: &net.IPNet{
							IP:   net.ParseIP("192.168.0.2"),
							Mask: net.CIDRMask(24, 32),
						},
						Scope: int(netlink.SCOPE_UNIVERSE),
					}
					err = netlink.AddrAdd(link, addr)
					Expect(err).NotTo(HaveOccurred())

					// Add a test route with specific gateway and Onlink flag to avoid gateway reachability check
					testRoute := &netlink.Route{
						Dst: &net.IPNet{
							IP:   net.ParseIP("10.0.0.0"),
							Mask: net.CIDRMask(8, 32),
						},
						Gw:        net.ParseIP("192.168.0.1"),
						LinkIndex: link.Attrs().Index,
						Flags:     int(netlink.FLAG_ONLINK),
					}

					err = netlink.RouteAdd(testRoute)
					Expect(err).NotTo(HaveOccurred())

					// Find the route with gateway filter
					searchRoute := &netlink.Route{
						Dst: &net.IPNet{
							IP:   net.ParseIP("10.0.0.0"),
							Mask: net.CIDRMask(8, 32),
						},
						Gw: net.ParseIP("192.168.0.1"),
					}

					routes, err := FoundRoutes(searchRoute)
					Expect(err).NotTo(HaveOccurred())
					Expect(len(routes)).To(BeNumerically(">", 0))

					// Verify found route matches
					found := false
					for _, route := range routes {
						if route.Dst.String() == testRoute.Dst.String() &&
							route.Gw.String() == testRoute.Gw.String() &&
							route.LinkIndex == testRoute.LinkIndex {
							found = true
							break
						}
					}
					Expect(found).To(BeTrue())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should return empty result for non-existent route", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					// Search for a non-existent route
					searchRoute := &netlink.Route{
						Dst: &net.IPNet{
							IP:   net.ParseIP("192.168.999.0"),
							Mask: net.CIDRMask(24, 32),
						},
					}

					routes, err := FoundRoutes(searchRoute)
					Expect(err).NotTo(HaveOccurred())
					Expect(len(routes)).To(Equal(0))

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("EnsureRoute", func() {
			It("should add route when not exists", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					// Set link up first
					err = netlink.LinkSetUp(link)
					Expect(err).NotTo(HaveOccurred())

					// Add IP address to the link first
					addr := &netlink.Addr{
						IPNet: &net.IPNet{
							IP:   net.ParseIP("192.168.10.2"),
							Mask: net.CIDRMask(24, 32),
						},
						Scope: int(netlink.SCOPE_UNIVERSE),
					}
					err = netlink.AddrAdd(link, addr)
					Expect(err).NotTo(HaveOccurred())

					expected := &netlink.Route{
						Dst: &net.IPNet{
							IP:   net.ParseIP("10.0.1.0"),
							Mask: net.CIDRMask(24, 32),
						},
						Gw:        net.ParseIP("192.168.10.1"),
						LinkIndex: link.Attrs().Index,
						Flags:     int(netlink.FLAG_ONLINK),
					}

					changed, err = EnsureRoute(context.Background(), expected)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeTrue())

					// Verify route exists
					routes, err := FoundRoutes(expected)
					Expect(err).NotTo(HaveOccurred())
					Expect(len(routes)).To(BeNumerically(">", 0))

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should not change when route already exists", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link, err := netlink.LinkByName(nicName)
					Expect(err).NotTo(HaveOccurred())

					// Set link up first
					err = netlink.LinkSetUp(link)
					Expect(err).NotTo(HaveOccurred())

					// Add IP address to the link first
					addr := &netlink.Addr{
						IPNet: &net.IPNet{
							IP:   net.ParseIP("192.168.20.2"),
							Mask: net.CIDRMask(24, 32),
						},
						Scope: int(netlink.SCOPE_UNIVERSE),
					}
					err = netlink.AddrAdd(link, addr)
					Expect(err).NotTo(HaveOccurred())

					expected := &netlink.Route{
						Dst: &net.IPNet{
							IP:   net.ParseIP("10.0.2.0"),
							Mask: net.CIDRMask(24, 32),
						},
						Gw:        net.ParseIP("192.168.20.1"),
						LinkIndex: link.Attrs().Index,
						Flags:     int(netlink.FLAG_ONLINK),
					}

					// Add route first
					err = netlink.RouteAdd(expected)
					Expect(err).NotTo(HaveOccurred())

					changed, err = EnsureRoute(context.Background(), expected)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeFalse())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})
	})

	Describe("IP Rule operations", func() {
		var hostNS ns.NetNS
		const nicName = "rule-test-nic"

		BeforeEach(func() {
			var err error
			hostNS, err = testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				return netlink.LinkAdd(&netlink.Dummy{
					LinkAttrs: netlink.LinkAttrs{Name: nicName},
				})
			})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			_ = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				link, err := netlink.LinkByName(nicName)
				if err == nil {
					err = netlink.LinkDel(link)
					Expect(err).NotTo(HaveOccurred())
				}
				return nil
			})
			Expect(hostNS.Close()).To(Succeed())
			Expect(testutils.UnmountNS(hostNS)).To(Succeed())
		})

		Describe("FindIPRule", func() {
			It("should find rule with source IP", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					// Add a rule first
					rule := netlink.NewRule()
					rule.Src = &net.IPNet{
						IP:   net.ParseIP("192.168.1.0"),
						Mask: net.CIDRMask(24, 32),
					}
					rule.Table = 100
					rule.Priority = 1000

					err = netlink.RuleAdd(rule)
					Expect(err).NotTo(HaveOccurred())

					// Find the rule
					rules, err := FindIPRule(rule)
					Expect(err).NotTo(HaveOccurred())
					Expect(len(rules)).To(BeNumerically(">", 0))

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("not found test", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					// Add a rule first
					rule := netlink.NewRule()
					rule.Src = &net.IPNet{
						IP:   net.ParseIP("192.168.1.0"),
						Mask: net.CIDRMask(24, 32),
					}
					rule.Table = 100
					rule.Priority = 1000
					rule.Family = netlink.FAMILY_V6
					rule.Mark = 0
					rule.Mask = 0

					err = netlink.RuleAdd(rule)
					Expect(err).NotTo(HaveOccurred())

					// Find the rule
					rules, err := FindIPRule(rule)
					Expect(err).NotTo(HaveOccurred())
					Expect(len(rules)).To(Equal(0))

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should return error for empty rule", func() {
				var err error

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					rule := &netlink.Rule{}

					_, err := FindIPRule(rule)
					Expect(err).To(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("EnsureIPRule", func() {
			It("should add rule when not exists", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					rule := netlink.NewRule()
					rule.Src = &net.IPNet{
						IP:   net.ParseIP("192.168.5.0"),
						Mask: net.CIDRMask(24, 32),
					}
					rule.Table = 200
					rule.Priority = 2000

					changed, err = EnsureIPRule(context.Background(), rule)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeTrue())

					// Verify rule is added
					rules, err := FindIPRule(rule)
					Expect(err).NotTo(HaveOccurred())
					Expect(len(rules)).To(BeNumerically(">", 0))

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should add rule when exists", func() {
				var err error
				var changed bool

				err = hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					rule := netlink.NewRule()
					rule.Src = &net.IPNet{
						IP:   net.ParseIP("192.168.5.0"),
						Mask: net.CIDRMask(24, 32),
					}
					rule.Table = 200
					rule.Priority = 2000

					changed, err = EnsureIPRule(context.Background(), rule)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeTrue())

					changed, err = EnsureIPRule(context.Background(), rule)
					Expect(err).NotTo(HaveOccurred())
					Expect(changed).To(BeFalse())
					// Verify rule is added
					rules, err := FindIPRule(rule)
					Expect(err).NotTo(HaveOccurred())
					Expect(len(rules)).To(BeNumerically(">", 0))

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

	})

	Describe("GenerateIPv6Sysctl", func() {
		It("should generate sysctl config for interface", func() {
			result := GenerateIPv6Sysctl("eth0", true, true)

			Expect(result).To(HaveKey("/proc/sys/net/ipv6/conf/lo/disable_ipv6"))
			Expect(result).To(HaveKey("/proc/sys/net/ipv6/conf/all/disable_ipv6"))
			Expect(result).To(HaveKey("/proc/sys/net/ipv6/conf/default/disable_ipv6"))
			Expect(result).To(HaveKey("/proc/sys/net/ipv6/conf/eth0/disable_ipv6"))
			Expect(result).To(HaveKey("/proc/sys/net/ipv6/conf/eth0/accept_ra"))
			Expect(result).To(HaveKey("/proc/sys/net/ipv6/conf/eth0/forwarding"))
		})

		It("should generate sysctl config without interface", func() {
			result := GenerateIPv6Sysctl("", false, false)

			Expect(result).To(HaveKey("/proc/sys/net/ipv6/conf/lo/disable_ipv6"))
			Expect(result).To(HaveKey("/proc/sys/net/ipv6/conf/all/disable_ipv6"))
			Expect(result).To(HaveKey("/proc/sys/net/ipv6/conf/default/disable_ipv6"))
			Expect(result).NotTo(HaveKey("/proc/sys/net/ipv6/conf/eth0/disable_ipv6"))
		})
	})

	Describe("EnsureNeigh", func() {
		var hostNS ns.NetNS
		const nicName = "neigh-test-nic"

		BeforeEach(func() {
			var err error
			hostNS, err = testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				return netlink.LinkAdd(&netlink.Dummy{
					LinkAttrs: netlink.LinkAttrs{Name: nicName},
				})
			})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			_ = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				link, err := netlink.LinkByName(nicName)
				if err == nil {
					err = netlink.LinkDel(link)
					Expect(err).NotTo(HaveOccurred())
				}
				return nil
			})
			Expect(hostNS.Close()).To(Succeed())
			Expect(testutils.UnmountNS(hostNS)).To(Succeed())
		})

		It("should add neighbor entry when not exists", func() {
			var err error
			var changed bool

			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()

				link, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())

				neigh := &netlink.Neigh{
					IP:           net.ParseIP("192.168.1.100"),
					HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
					LinkIndex:    link.Attrs().Index,
					State:        netlink.NUD_PERMANENT,
				}

				changed, err = EnsureNeigh(context.Background(), neigh)
				Expect(err).NotTo(HaveOccurred())
				Expect(changed).To(BeTrue())

				// Verify neighbor is added
				neighs, err := netlink.NeighList(link.Attrs().Index, netlink.FAMILY_V4)
				Expect(err).NotTo(HaveOccurred())

				found := false
				for _, n := range neighs {
					if n.IP.Equal(neigh.IP) && n.HardwareAddr.String() == neigh.HardwareAddr.String() {
						found = true
						break
					}
				}
				Expect(found).To(BeTrue())

				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should not add neighbor when already exists", func() {
			var err error
			var changed bool

			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()

				link, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())

				neigh := &netlink.Neigh{
					IP:           net.ParseIP("192.168.1.200"),
					HardwareAddr: net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
					LinkIndex:    link.Attrs().Index,
					State:        netlink.NUD_PERMANENT,
				}

				// Add neighbor first
				err = netlink.NeighSet(neigh)
				Expect(err).NotTo(HaveOccurred())

				// Try to ensure same neighbor
				changed, err = EnsureNeigh(context.Background(), neigh)
				Expect(err).NotTo(HaveOccurred())
				Expect(changed).To(BeFalse())

				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("TC filter operations", func() {
		var hostNS ns.NetNS
		const nicName = "eni"

		BeforeEach(func() {
			var err error
			hostNS, err = testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				return netlink.LinkAdd(&netlink.GenericLink{
					LinkAttrs: netlink.LinkAttrs{
						Name:        nicName,
						TxQLen:      1000,
						NumTxQueues: 2,
						NumRxQueues: 2,
					},
					LinkType: "veth",
				})
			})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			_ = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				eni, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())
				err = netlink.LinkDel(eni)
				Expect(err).NotTo(HaveOccurred())
				return nil
			})
			Expect(hostNS.Close()).To(Succeed())
			Expect(testutils.UnmountNS(hostNS)).To(Succeed())
		})

		It("should add qdisc mq", func() {
			var err error
			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				eni, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())

				err = EnsureMQQdisc(context.Background(), eni)
				Expect(err).NotTo(HaveOccurred())

				qds, err := netlink.QdiscList(eni)
				Expect(err).NotTo(HaveOccurred())

				found := false
				for _, qd := range qds {
					if qd.Type() != "mq" {
						continue
					}
					Expect(qd.Attrs().Parent).Should(Equal(uint32(netlink.HANDLE_ROOT)))
					Expect(qd.Attrs().Handle).Should(Equal(netlink.MakeHandle(1, 0)))
					found = true
					break
				}
				Expect(found).Should(BeTrue())
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should replace qdisc mq", func() {
			var err error
			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				eni, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())

				err = EnsureMQQdisc(context.Background(), eni)
				Expect(err).NotTo(HaveOccurred())

				err = EnsureMQQdisc(context.Background(), eni)
				Expect(err).NotTo(HaveOccurred())

				qds, err := netlink.QdiscList(eni)
				Expect(err).NotTo(HaveOccurred())

				found := false
				for _, qd := range qds {
					if qd.Type() != "mq" {
						continue
					}
					Expect(qd.Attrs().Parent).Should(Equal(uint32(netlink.HANDLE_ROOT)))
					Expect(qd.Attrs().Handle).Should(Equal(netlink.MakeHandle(1, 0)))
					found = true
					break
				}
				Expect(found).Should(BeTrue())
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should set egress filter", func() {
			var err error
			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()

				eni, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())

				err = SetEgressPriority(context.Background(), eni, netlink.MakeHandle(1, 1), &terwayTypes.IPNetSet{
					IPv4: &net.IPNet{
						IP:   net.ParseIP("192.168.1.1"),
						Mask: net.CIDRMask(32, 32),
					},
					IPv6: &net.IPNet{
						IP:   net.ParseIP("fd00::1"),
						Mask: net.CIDRMask(128, 128),
					},
				})
				Expect(err).NotTo(HaveOccurred())

				qds, err := netlink.QdiscList(eni)
				Expect(err).NotTo(HaveOccurred())

				for _, qd := range qds {
					_, ok := qd.(*netlink.Prio)
					if !ok {
						continue
					}

					u32, err := tc.FilterBySrcIP(eni, qd.Attrs().Handle, &net.IPNet{
						IP:   net.ParseIP("192.168.1.1"),
						Mask: net.CIDRMask(32, 32),
					})
					Expect(err).NotTo(HaveOccurred())
					Expect(u32).NotTo(BeNil(), "tc filter with src ipv4 should be found. Qdisc %#v", qd)

					u32v6, err := tc.FilterBySrcIP(eni, qd.Attrs().Handle, &net.IPNet{
						IP:   net.ParseIP("fd00::1"),
						Mask: net.CIDRMask(128, 128),
					})
					Expect(err).NotTo(HaveOccurred())
					Expect(u32v6).NotTo(BeNil(), "tc filter with src ipv6 should be found")
				}
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should add vlan tag", func() {
			var err error
			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				eni, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())

				By("add new ip", func() {
					err = EnsureVlanTag(context.Background(), eni, &terwayTypes.IPNetSet{
						IPv4: &net.IPNet{
							IP:   net.ParseIP("192.168.1.1"),
							Mask: net.CIDRMask(32, 32),
						},
						IPv6: &net.IPNet{
							IP:   net.ParseIP("fd00::1"),
							Mask: net.CIDRMask(128, 128),
						},
					}, 1023)
					Expect(err).NotTo(HaveOccurred())

					filters, err := netlink.FilterList(eni, netlink.HANDLE_MIN_EGRESS)
					Expect(err).NotTo(HaveOccurred())

					Expect(len(filters)).Should(Equal(2))
				})

				By("re-add same ip should succeed", func() {
					err = EnsureVlanTag(context.Background(), eni, &terwayTypes.IPNetSet{
						IPv4: &net.IPNet{
							IP:   net.ParseIP("192.168.1.1"),
							Mask: net.CIDRMask(32, 32),
						},
						IPv6: &net.IPNet{
							IP:   net.ParseIP("fd00::1"),
							Mask: net.CIDRMask(128, 128),
						},
					}, 1023)
					Expect(err).NotTo(HaveOccurred())

					filters, err := netlink.FilterList(eni, netlink.HANDLE_MIN_EGRESS)
					Expect(err).NotTo(HaveOccurred())

					Expect(len(filters)).Should(Equal(2))
				})

				By("add new rule should success", func() {
					err = EnsureVlanTag(context.Background(), eni, &terwayTypes.IPNetSet{
						IPv4: &net.IPNet{
							IP:   net.ParseIP("192.168.1.2"),
							Mask: net.CIDRMask(32, 32),
						},
						IPv6: &net.IPNet{
							IP:   net.ParseIP("fd00::2"),
							Mask: net.CIDRMask(128, 128),
						},
					}, 1022)
					Expect(err).NotTo(HaveOccurred())

					filters, err := netlink.FilterList(eni, netlink.HANDLE_MIN_EGRESS)
					Expect(err).NotTo(HaveOccurred())

					Expect(len(filters)).Should(Equal(4))
				})

				By("delete filter should clean up", func() {
					err = DelFilter(context.Background(), eni, netlink.HANDLE_MIN_EGRESS, &terwayTypes.IPNetSet{
						IPv4: &net.IPNet{
							IP:   net.ParseIP("192.168.1.2"),
							Mask: net.CIDRMask(32, 32),
						},
						IPv6: &net.IPNet{
							IP:   net.ParseIP("fd00::2"),
							Mask: net.CIDRMask(128, 128),
						},
					})
					Expect(err).NotTo(HaveOccurred())

					filters, err := netlink.FilterList(eni, netlink.HANDLE_MIN_EGRESS)
					Expect(err).NotTo(HaveOccurred())

					Expect(len(filters)).Should(Equal(2))
				})

				By("add same ip vid should be updated", func() {
					err = EnsureVlanTag(context.Background(), eni, &terwayTypes.IPNetSet{
						IPv4: &net.IPNet{
							IP:   net.ParseIP("192.168.1.1"),
							Mask: net.CIDRMask(32, 32),
						},
						IPv6: &net.IPNet{
							IP:   net.ParseIP("fd00::1"),
							Mask: net.CIDRMask(128, 128),
						},
					}, 4000)
					Expect(err).NotTo(HaveOccurred())

					filters, err := netlink.FilterList(eni, netlink.HANDLE_MIN_EGRESS)
					Expect(err).NotTo(HaveOccurred())

					Expect(len(filters)).Should(Equal(2))

					for _, filter := range filters {
						u32 := filter.(*netlink.U32)
						Expect(len(u32.Actions)).Should(Equal(1))

						act := u32.Actions[0].(*netlink.VlanAction)

						Expect(act.Attrs().Action).Should(Equal(netlink.TC_ACT_PIPE))
						Expect(act.Action).Should(Equal(netlink.TCA_VLAN_KEY_PUSH))
						Expect(act.Vid).Should(Equal(uint16(4000)))
					}
				})
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("ERdma link operations", func() {
		It("should parse ERdma link hardware address correctly", func() {
			hwaddr, err := parseERdmaLinkHwAddr("0d:d3:04:fe:ff:3e:16:02")
			Expect(err).NotTo(HaveOccurred())
			Expect(hwaddr.String()).To(Equal("02:16:3e:04:d3:0d"))
		})
	})

	Describe("Netlink operations", func() {
		var hostNS ns.NetNS
		const testLinkName = "test-link"

		BeforeEach(func() {
			var err error
			hostNS, err = testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			Expect(hostNS.Close()).To(Succeed())
			Expect(testutils.UnmountNS(hostNS)).To(Succeed())
		})

		Describe("LinkAdd", func() {
			It("should add link successfully", func() {
				err := hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					link := &netlink.Dummy{
						LinkAttrs: netlink.LinkAttrs{Name: testLinkName},
					}

					err := LinkAdd(context.Background(), link)
					Expect(err).NotTo(HaveOccurred())

					// Verify link was created
					createdLink, err := netlink.LinkByName(testLinkName)
					Expect(err).NotTo(HaveOccurred())
					Expect(createdLink.Attrs().Name).To(Equal(testLinkName))

					// Clean up
					err = netlink.LinkDel(createdLink)
					Expect(err).NotTo(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should return error for invalid link", func() {
				err := hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					// Create a link with invalid attributes
					link := &netlink.Dummy{
						LinkAttrs: netlink.LinkAttrs{Name: ""}, // Empty name should cause error
					}

					err := LinkAdd(context.Background(), link)
					Expect(err).To(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("LinkSetDown", func() {
			It("should set link down successfully", func() {
				err := hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					// Create a link first
					link := &netlink.Dummy{
						LinkAttrs: netlink.LinkAttrs{Name: testLinkName},
					}
					err := netlink.LinkAdd(link)
					Expect(err).NotTo(HaveOccurred())

					// Set link up first
					err = netlink.LinkSetUp(link)
					Expect(err).NotTo(HaveOccurred())

					// Verify link is up
					createdLink, err := netlink.LinkByName(testLinkName)
					Expect(err).NotTo(HaveOccurred())
					Expect(createdLink.Attrs().Flags & net.FlagUp).NotTo(Equal(0))

					// Set link down
					err = LinkSetDown(context.Background(), createdLink)
					Expect(err).NotTo(HaveOccurred())

					// Verify link is down
					downLink, err := netlink.LinkByName(testLinkName)
					Expect(err).NotTo(HaveOccurred())
					Expect(downLink.Attrs().Flags & net.FlagUp).To(Equal(net.Flags(0)))

					// Clean up
					err = netlink.LinkDel(createdLink)
					Expect(err).NotTo(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("RouteReplace", func() {
			It("should replace route successfully", func() {
				err := hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					// Create a dummy link for the route
					link := &netlink.Dummy{
						LinkAttrs: netlink.LinkAttrs{Name: testLinkName},
					}
					err := netlink.LinkAdd(link)
					Expect(err).NotTo(HaveOccurred())

					// Set link up
					err = netlink.LinkSetUp(link)
					Expect(err).NotTo(HaveOccurred())

					// Add an IP address to the link
					addr := &netlink.Addr{
						IPNet: &net.IPNet{
							IP:   net.ParseIP("192.168.1.1"),
							Mask: net.CIDRMask(24, 32),
						},
					}
					err = netlink.AddrAdd(link, addr)
					Expect(err).NotTo(HaveOccurred())

					// Create a route
					route := &netlink.Route{
						Dst: &net.IPNet{
							IP:   net.ParseIP("10.0.0.0"),
							Mask: net.CIDRMask(8, 32),
						},
						Gw:        net.ParseIP("192.168.1.254"),
						LinkIndex: link.Attrs().Index,
					}

					// Replace the route
					err = RouteReplace(context.Background(), route)
					Expect(err).NotTo(HaveOccurred())

					// Verify route exists
					routes, err := netlink.RouteList(link, netlink.FAMILY_V4)
					Expect(err).NotTo(HaveOccurred())
					Expect(len(routes)).To(BeNumerically(">", 0))

					// Clean up
					err = netlink.LinkDel(link)
					Expect(err).NotTo(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("RouteDel", func() {
			It("should delete route successfully", func() {
				err := hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					// Create a dummy link for the route
					link := &netlink.Dummy{
						LinkAttrs: netlink.LinkAttrs{Name: testLinkName},
					}
					err := netlink.LinkAdd(link)
					Expect(err).NotTo(HaveOccurred())

					// Set link up
					err = netlink.LinkSetUp(link)
					Expect(err).NotTo(HaveOccurred())

					// Add an IP address to the link
					addr := &netlink.Addr{
						IPNet: &net.IPNet{
							IP:   net.ParseIP("192.168.1.1"),
							Mask: net.CIDRMask(24, 32),
						},
					}
					err = netlink.AddrAdd(link, addr)
					Expect(err).NotTo(HaveOccurred())

					// Create and add a route
					route := &netlink.Route{
						Dst: &net.IPNet{
							IP:   net.ParseIP("10.0.0.0"),
							Mask: net.CIDRMask(8, 32),
						},
						Gw:        net.ParseIP("192.168.1.254"),
						LinkIndex: link.Attrs().Index,
					}
					err = netlink.RouteAdd(route)
					Expect(err).NotTo(HaveOccurred())

					// Verify route exists
					routes, err := netlink.RouteList(link, netlink.FAMILY_V4)
					Expect(err).NotTo(HaveOccurred())
					initialRouteCount := len(routes)

					// Delete the route
					err = RouteDel(context.Background(), route)
					Expect(err).NotTo(HaveOccurred())

					// Verify route was deleted
					routes, err = netlink.RouteList(link, netlink.FAMILY_V4)
					Expect(err).NotTo(HaveOccurred())
					Expect(len(routes)).To(BeNumerically("<", initialRouteCount))

					// Clean up
					err = netlink.LinkDel(link)
					Expect(err).NotTo(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("LinkSetNsFd", func() {
			It("should move link to namespace successfully", func() {
				err := hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					// Create a container namespace
					containerNS, err := testutils.NewNS()
					Expect(err).NotTo(HaveOccurred())
					defer func() {
						Expect(containerNS.Close()).To(Succeed())
						Expect(testutils.UnmountNS(containerNS)).To(Succeed())
					}()

					// Create a dummy link
					link := &netlink.Dummy{
						LinkAttrs: netlink.LinkAttrs{Name: testLinkName},
					}
					err = netlink.LinkAdd(link)
					Expect(err).NotTo(HaveOccurred())

					// Get the created link
					createdLink, err := netlink.LinkByName(testLinkName)
					Expect(err).NotTo(HaveOccurred())

					// Move link to container namespace
					err = LinkSetNsFd(context.Background(), createdLink, containerNS)
					Expect(err).NotTo(HaveOccurred())

					// Verify link no longer exists in current namespace
					_, err = netlink.LinkByName(testLinkName)
					Expect(err).To(HaveOccurred())

					// Verify link exists in container namespace
					err = containerNS.Do(func(ns ns.NetNS) error {
						_, err := netlink.LinkByName(testLinkName)
						Expect(err).NotTo(HaveOccurred())
						return nil
					})
					Expect(err).NotTo(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Describe("QdiscDel", func() {
			It("should delete qdisc successfully", func() {
				err := hostNS.Do(func(netNS ns.NetNS) error {
					defer GinkgoRecover()

					// Create a dummy link
					link := &netlink.Dummy{
						LinkAttrs: netlink.LinkAttrs{Name: testLinkName},
					}
					err := netlink.LinkAdd(link)
					Expect(err).NotTo(HaveOccurred())

					// Set link up
					err = netlink.LinkSetUp(link)
					Expect(err).NotTo(HaveOccurred())

					// Create a qdisc
					qdisc := &netlink.PfifoFast{
						QdiscAttrs: netlink.QdiscAttrs{
							LinkIndex: link.Attrs().Index,
							Handle:    netlink.MakeHandle(1, 0),
							Parent:    netlink.HANDLE_ROOT,
						},
					}

					// Add the qdisc
					err = netlink.QdiscAdd(qdisc)
					Expect(err).NotTo(HaveOccurred())

					// Delete the qdisc
					err = QdiscDel(context.Background(), qdisc)
					Expect(err).NotTo(HaveOccurred())

					// Clean up
					err = netlink.LinkDel(link)
					Expect(err).NotTo(HaveOccurred())

					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})
	})

	Describe("DelEgressPriority", func() {
		var hostNS ns.NetNS
		const nicName = "test-nic-prio"

		BeforeEach(func() {
			var err error
			hostNS, err = testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				return netlink.LinkAdd(&netlink.Dummy{
					LinkAttrs: netlink.LinkAttrs{Name: nicName},
				})
			})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			_ = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				link, err := netlink.LinkByName(nicName)
				if err == nil {
					err = netlink.LinkDel(link)
					Expect(err).NotTo(HaveOccurred())
				}
				return nil
			})
			Expect(hostNS.Close()).To(Succeed())
			Expect(testutils.UnmountNS(hostNS)).To(Succeed())
		})

		It("should handle empty qdisc list", func() {
			err := hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				link, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())

				ipNetSet := &terwayTypes.IPNetSet{}
				ipNetSet.SetIPNet("192.168.1.0/24")

				err = DelEgressPriority(context.Background(), link, ipNetSet)
				Expect(err).NotTo(HaveOccurred())
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should handle link with prio qdisc", func() {
			err := hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				link, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())

				// Set link up first
				err = netlink.LinkSetUp(link)
				Expect(err).NotTo(HaveOccurred())

				// Add a prio qdisc
				prio := &netlink.Prio{
					QdiscAttrs: netlink.QdiscAttrs{
						LinkIndex: link.Attrs().Index,
						Parent:    netlink.HANDLE_ROOT,
						Handle:    netlink.MakeHandle(1, 0),
					},
					Bands: 3,
				}
				err = netlink.QdiscAdd(prio)
				if err != nil {
					// Ignore error if qdisc already exists
					Skip("Cannot add prio qdisc, skipping test")
				}

				ipNetSet := &terwayTypes.IPNetSet{}
				ipNetSet.SetIPNet("192.168.1.0/24")

				err = DelEgressPriority(context.Background(), link, ipNetSet)
				Expect(err).NotTo(HaveOccurred())
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("SetupTC", func() {
		var hostNS ns.NetNS
		const nicName = "test-nic-tc"

		BeforeEach(func() {
			var err error
			hostNS, err = testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				return netlink.LinkAdd(&netlink.Dummy{
					LinkAttrs: netlink.LinkAttrs{
						Name: nicName,
						MTU:  1500,
					},
				})
			})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			_ = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				link, err := netlink.LinkByName(nicName)
				if err == nil {
					err = netlink.LinkDel(link)
					Expect(err).NotTo(HaveOccurred())
				}
				return nil
			})
			Expect(hostNS.Close()).To(Succeed())
			Expect(testutils.UnmountNS(hostNS)).To(Succeed())
		})

		It("should setup traffic control successfully", func() {
			err := hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				link, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())

				// Set link up first
				err = netlink.LinkSetUp(link)
				Expect(err).NotTo(HaveOccurred())

				// Test with valid bandwidth
				bandwidthInBytes := uint64(1000000) // 1MB/s
				err = SetupTC(link, bandwidthInBytes)
				Expect(err).NotTo(HaveOccurred())

				// Verify qdisc was added
				qdiscs, err := netlink.QdiscList(link)
				Expect(err).NotTo(HaveOccurred())
				Expect(len(qdiscs)).To(BeNumerically(">", 0))
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return error for invalid bandwidth", func() {
			err := hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				link, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())

				// Test with zero bandwidth
				err = SetupTC(link, 0)
				Expect(err).To(HaveOccurred())
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("GenericTearDown", func() {
		var hostNS, containerNS ns.NetNS

		BeforeEach(func() {
			var err error
			hostNS, err = testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			containerNS, err = testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			// Set host NS as current
			err = hostNS.Set()
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			Expect(containerNS.Close()).To(Succeed())
			Expect(testutils.UnmountNS(containerNS)).To(Succeed())
			Expect(hostNS.Close()).To(Succeed())
			Expect(testutils.UnmountNS(hostNS)).To(Succeed())
		})

		It("should clean up virtual interfaces", func() {
			// Add various types of interfaces to container NS
			err := containerNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()

				// Add dummy interface
				err := netlink.LinkAdd(&netlink.Dummy{
					LinkAttrs: netlink.LinkAttrs{Name: "dummy0"},
				})
				Expect(err).NotTo(HaveOccurred())

				// Add veth pair
				veth := &netlink.Veth{
					LinkAttrs: netlink.LinkAttrs{Name: "veth0"},
					PeerName:  "veth1",
				}
				err = netlink.LinkAdd(veth)
				if err == nil {
					// Set up the interfaces
					veth0, _ := netlink.LinkByName("veth0")
					if veth0 != nil {
						netlink.LinkSetUp(veth0)
					}
					veth1, _ := netlink.LinkByName("veth1")
					if veth1 != nil {
						netlink.LinkSetUp(veth1)
					}
				}

				return nil
			})
			Expect(err).NotTo(HaveOccurred())

			// Perform teardown
			_ = GenericTearDown(context.Background(), containerNS)

			// Verify interfaces were cleaned up
			err = containerNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				links, err := netlink.LinkList()
				Expect(err).NotTo(HaveOccurred())

				// Should only have loopback interface left
				nonLoLinks := 0
				for _, link := range links {
					if link.Attrs().Name != "lo" {
						nonLoLinks++
					}
				}
				Expect(nonLoLinks).To(Equal(0))
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should handle empty namespace", func() {
			// Test teardown on empty namespace (only loopback)
			err := GenericTearDown(context.Background(), containerNS)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("CleanIPRules", func() {
		var hostNS ns.NetNS

		BeforeEach(func() {
			var err error
			hostNS, err = testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			Expect(hostNS.Close()).To(Succeed())
			Expect(testutils.UnmountNS(hostNS)).To(Succeed())
		})

		It("should clean IP rules successfully", func() {
			err := hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()

				// Add some test rules with priority 512 and 2048
				rule512 := netlink.NewRule()
				rule512.Priority = 512
				rule512.Dst = &net.IPNet{
					IP:   net.ParseIP("192.168.1.0"),
					Mask: net.CIDRMask(24, 32),
				}
				rule512.Table = 100

				rule2048 := netlink.NewRule()
				rule2048.Priority = 2048
				rule2048.Src = &net.IPNet{
					IP:   net.ParseIP("10.0.0.0"),
					Mask: net.CIDRMask(8, 32),
				}
				rule2048.Table = 200

				// Add rules (ignore errors if they already exist)
				_ = netlink.RuleAdd(rule512)
				_ = netlink.RuleAdd(rule2048)

				// Clean the rules
				err := CleanIPRules(context.Background())
				Expect(err).NotTo(HaveOccurred())

				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should handle rules with different priorities", func() {
			err := hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()

				// Add rule with different priority (should not be cleaned)
				rule1000 := netlink.NewRule()
				rule1000.Priority = 1000
				rule1000.Dst = &net.IPNet{
					IP:   net.ParseIP("172.16.0.0"),
					Mask: net.CIDRMask(12, 32),
				}
				rule1000.Table = 300

				_ = netlink.RuleAdd(rule1000)

				// Clean the rules
				err := CleanIPRules(context.Background())
				Expect(err).NotTo(HaveOccurred())

				// Verify rule with priority 1000 still exists
				rules, err := netlink.RuleList(netlink.FAMILY_V4)
				Expect(err).NotTo(HaveOccurred())

				found := false
				for _, r := range rules {
					if r.Priority == 1000 {
						found = true
						break
					}
				}
				Expect(found).To(BeTrue())

				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should handle rules with different oif", func() {
			err := hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()

				// Add rule with different priority (should not be cleaned)
				rule1000 := netlink.NewRule()
				rule1000.Priority = 1000
				rule1000.Dst = &net.IPNet{
					IP:   net.ParseIP("172.16.0.0"),
					Mask: net.CIDRMask(12, 32),
				}
				rule1000.Table = 300
				rule1000.OifName = "dummy"

				_ = netlink.RuleAdd(rule1000)

				// Clean the rules
				err := CleanIPRules(context.Background())
				Expect(err).NotTo(HaveOccurred())

				// Verify rule with priority 1000 still exists
				rules, err := netlink.RuleList(netlink.FAMILY_V4)
				Expect(err).NotTo(HaveOccurred())

				found := false
				for _, r := range rules {
					if r.Priority == 1000 {
						found = true
						break
					}
				}
				Expect(found).To(BeTrue())

				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("GetERdmaFromLink", func() {
		var hostNS ns.NetNS
		const nicName = "test-nic-erdma"

		BeforeEach(func() {
			var err error
			hostNS, err = testutils.NewNS()
			Expect(err).NotTo(HaveOccurred())

			err = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				return netlink.LinkAdd(&netlink.Dummy{
					LinkAttrs: netlink.LinkAttrs{
						Name:         nicName,
						HardwareAddr: net.HardwareAddr{0x02, 0x42, 0xac, 0x11, 0x00, 0x02},
					},
				})
			})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			_ = hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				link, err := netlink.LinkByName(nicName)
				if err == nil {
					err = netlink.LinkDel(link)
					Expect(err).NotTo(HaveOccurred())
				}
				return nil
			})
			Expect(hostNS.Close()).To(Succeed())
			Expect(testutils.UnmountNS(hostNS)).To(Succeed())
		})

		It("should return error when no RDMA links found", func() {
			err := hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				link, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())

				// This should return error since we don't have real RDMA hardware
				_, err = GetERdmaFromLink(link)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("cannot found rdma link"))

				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should handle netlink.RdmaLinkList error gracefully", func() {
			err := hostNS.Do(func(netNS ns.NetNS) error {
				defer GinkgoRecover()
				link, err := netlink.LinkByName(nicName)
				Expect(err).NotTo(HaveOccurred())

				// This will likely fail due to no RDMA support in test environment
				// but should handle the error gracefully
				rdmaLink, err := GetERdmaFromLink(link)
				if err != nil {
					// Expected in test environment without RDMA hardware
					Expect(err.Error()).To(SatisfyAny(
						ContainSubstring("error list rdma links"),
						ContainSubstring("cannot found rdma link"),
					))
					Expect(rdmaLink).To(BeNil())
				}

				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("test GetERdmaFromLink with mock RDMA link", func() {

			link := &netlink.Dummy{
				LinkAttrs: netlink.LinkAttrs{
					Name:         "mock-rdma-link",
					HardwareAddr: net.HardwareAddr{0xb9, 0xaa, 0x99, 0x66, 0x1c, 0x02},
				},
			}
			rdmaLink := &netlink.RdmaLink{
				Attrs: netlink.RdmaLinkAttrs{
					Name:     "mock-rdma-link",
					NodeGuid: "02:1c:66:77:88:99:aa:bb",
				},
			}
			patches := gomonkey.ApplyFunc(netlink.RdmaLinkList, func() ([]*netlink.RdmaLink, error) {
				return []*netlink.RdmaLink{
					rdmaLink,
				}, nil
			})

			defer patches.Reset()
			_, err := GetERdmaFromLink(link)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
