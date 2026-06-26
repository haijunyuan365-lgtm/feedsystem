package account

import (
	"context"
	"errors"
	"feedsystem/internal/auth"
	rediscache "feedsystem/internal/middleware/redis"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

type AccountService struct {
	accountRepository *AccountRepository
	cache             *rediscache.Client
}

var (
	ErrUsernameRequired    = errors.New("username is required")
	ErrPasswordRequired    = errors.New("password is required")
	ErrUsernameTaken       = errors.New("username already exists")
	ErrNewUsernameRequired = errors.New("new_username is required")
)

func NewAccountService(accountRepository *AccountRepository, cache *rediscache.Client) *AccountService {
	return &AccountService{
		accountRepository: accountRepository,
		cache:             cache,
	}
}

func (as *AccountService) CreateAccount(ctx context.Context, account *Account) error {
	account.Username = strings.TrimSpace(account.Username)
	if account.Username == "" {
		return ErrUsernameRequired
	}
	if account.Password == "" {
		return ErrPasswordRequired
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(account.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	account.Password = string(passwordHash)

	if err := as.accountRepository.CreateAccount(ctx, account); err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return ErrUsernameTaken
		}
		return err
	}
	return nil
}

func (as *AccountService) FindByID(ctx context.Context, id uint) (*Account, error) {
	return as.accountRepository.FindByID(ctx, id)
}

func (as *AccountService) FindByUsername(ctx context.Context, username string) (*Account, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, ErrUsernameRequired
	}
	return as.accountRepository.FindByUsername(ctx, username)
}

func (as *AccountService) Login(ctx context.Context, username, password string) (string, string, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return "", "", ErrUsernameRequired
	}
	if password == "" {
		return "", "", ErrPasswordRequired
	}

	account, err := as.accountRepository.FindByUsername(ctx, username)
	if err != nil {
		return "", "", err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(account.Password), []byte(password)); err != nil {
		return "", "", err
	}

	accessToken, err := auth.GenerateToken(account.ID, account.Username)
	if err != nil {
		return "", "", err
	}

	refreshToken, err := auth.GenerateRefreshToken(account.ID)
	if err != nil {
		return "", "", err
	}

	//保存accessToken和refreshToken到mysql
	if err := as.accountRepository.Login(ctx, account.ID, accessToken, refreshToken); err != nil {
		return "", "", err
	}

	//保存accessToken和refreshToken到redis，以及生成了一个反向查询：通过 refreshToken 查用户 ID
	if as.cache != nil {
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		//redis中设置的token过期时间尽量和jwt中签发的过期时间相同
		if err := as.cache.SetBytes(cacheCtx, as.cache.Key("account:%d", account.ID), []byte(accessToken), 15*time.Minute); err != nil {
			log.Printf("failed to set access token cache: %v", err)
		}

		if err := as.cache.SetBytes(cacheCtx, as.cache.Key("account:%d:refresh", account.ID), []byte(refreshToken), 7*24*time.Hour); err != nil {
			log.Printf("failed to set refresh token cache: %v", err)
		}

		if err := as.cache.SetBytes(cacheCtx, as.cache.Key("refresh:%s", refreshToken), []byte(strconv.FormatUint(uint64(account.ID), 10)), 7*24*time.Hour); err != nil {
			log.Printf("failed to set refresh lookup cache: %v", err)
		}
	}

	return accessToken, refreshToken, nil
}

func (as *AccountService) Logout(ctx context.Context, accountID uint) error {
	account, err := as.FindByID(ctx, accountID)
	if err != nil {
		return err
	}

	if account.Token == "" {
		return nil
	}

	if as.cache != nil {
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()

		if err := as.cache.Del(cacheCtx, as.cache.Key("account:%d", account.ID)); err != nil {
			log.Printf("failed to delete access token cache: %v", err)
		}

		if err := as.cache.Del(cacheCtx, as.cache.Key("account:%d:refresh", account.ID)); err != nil {
			log.Printf("failed to delete refresh token cache: %v", err)
		}

		if account.RefreshToken != "" {
			if err := as.cache.Del(cacheCtx, as.cache.Key("refresh:%s", account.RefreshToken)); err != nil {
				log.Printf("failed to delete refresh lookup cache: %v", err)
			}
		}
	}

	return as.accountRepository.Logout(ctx, account.ID)
}

func (as *AccountService) Rename(ctx context.Context, accountID uint, newUsername string) (string, error) {
	newUsername = strings.TrimSpace(newUsername)
	if newUsername == "" {
		return "", ErrNewUsernameRequired
	}

	token, err := auth.GenerateToken(accountID, newUsername)
	if err != nil {
		return "", err
	}

	if err := as.accountRepository.RenameWithToken(ctx, accountID, newUsername, token); err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return "", ErrUsernameTaken
		}
		return "", err
	}

	if as.cache != nil {
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()

		if err := as.cache.SetBytes(cacheCtx, as.cache.Key("account:%d", accountID), []byte(token), 24*time.Hour); err != nil {
			log.Printf("failed to set renamed token cache: %v", err)
		}
	}

	return token, nil
}

func (as *AccountService) UpdateAvatar(ctx context.Context, accountID uint, avatarURL string) error {
	return as.accountRepository.UpdateAvatar(ctx, accountID, avatarURL)
}

func (as *AccountService) UpdateProfile(ctx context.Context, accountID uint, req *UpdateProfileRequest) error {
	if req == nil {
		return errors.New("request is nil")
	}
	//等价于 updates := make(map[string]interface{})
	updates := map[string]interface{}{}
	//var updates map[string]interface{}
	//因为这样声明出来的是一个 nil map，不能直接赋值

	if req.Bio != "" {
		updates["bio"] = strings.TrimSpace(req.Bio)
	}

	if req.AvatarURL != "" {
		updates["avatar_url"] = strings.TrimSpace(req.AvatarURL)
	}

	if len(updates) == 0 {
		return errors.New("nothing to update")
	}

	return as.accountRepository.UpdateFields(ctx, accountID, updates)
}

func (as *AccountService) RefreshAccessToken(ctx context.Context, refreshToken string) (string, uint, string, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return "", 0, "", errors.New("refresh token is empty")
	}

	if as.cache != nil {
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()

		b, err := as.cache.GetBytes(cacheCtx, as.cache.Key("refresh:%s", refreshToken))
		if err == nil {
			id, parseErr := strconv.ParseUint(string(b), 10, 64)
			if parseErr == nil {
				account, err := as.FindByID(ctx, uint(id))
				if err == nil && account != nil && account.RefreshToken == refreshToken {
					newToken, err := auth.GenerateToken(account.ID, account.Username)
					if err != nil {
						return "", 0, "", err
					}

					if err := as.accountRepository.UpdateToken(ctx, account.ID, newToken); err != nil {
						return "", 0, "", err
					}

					if err := as.cache.SetBytes(cacheCtx, as.cache.Key("account:%d", account.ID), []byte(newToken), 24*time.Hour); err != nil {
						log.Printf("failed to set refreshed access token cache: %v", err)
					}

					return newToken, account.ID, account.Username, nil
				}
			}
		}
	}
	//查询所有用户，遍历兜底，不推荐
	accounts, err := as.accountRepository.FindAll(ctx)
	if err != nil {
		return "", 0, "", err
	}

	for _, acc := range accounts {
		if acc.RefreshToken == refreshToken {
			newToken, err := auth.GenerateToken(acc.ID, acc.Username)
			if err != nil {
				return "", 0, "", err
			}

			if err := as.accountRepository.UpdateToken(ctx, acc.ID, newToken); err != nil {
				return "", 0, "", err
			}

			if as.cache != nil {
				cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()

				if err := as.cache.SetBytes(cacheCtx, as.cache.Key("account:%d", acc.ID), []byte(newToken), 24*time.Hour); err != nil {
					log.Printf("failed to set refreshed access token cache: %v", err)
				}
			}

			return newToken, acc.ID, acc.Username, nil
		}
	}

	return "", 0, "", errors.New("invalid refresh token")
}

func (as *AccountService) ChangePassword(ctx context.Context, accountID uint, oldPassword, newPassword string) error {
	account, err := as.FindByID(ctx, accountID)
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(account.Password), []byte(oldPassword)); err != nil {
		return err
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := as.accountRepository.ChangePassword(ctx, account.ID, string(passwordHash)); err != nil {
		return err
	}
	if err := as.Logout(ctx, account.ID); err != nil {
		return err
	}
	return nil
}
